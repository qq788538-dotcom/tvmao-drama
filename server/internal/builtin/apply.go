package builtin

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// Apply 把编译进二进制的种子合并进库。sidecar 每次启动在 db.Migrate 之后、
// 开始监听之前调用一次。
//
// builtin_seed_state 里记录的哈希与本次种子相同就直接返回，不拿写锁。否则在一个写事务里完成全部合并：
// 首次启动就是“与空库合并”，和升级走同一套算法；任何一步失败整体回滚、哈希不写入，下次启动重试。
func Apply(conn *sql.DB) error {
	s, err := Load()
	if err != nil {
		return fmt.Errorf("解析内置种子: %w", err)
	}
	return applySeed(context.Background(), conn, s)
}

func applySeed(ctx context.Context, conn *sql.DB, s *Seed) error {
	h, ok, err := readSeedHash(ctx, conn, DatasetModelCenter)
	if err != nil {
		return err
	}
	if ok && h == s.Hash {
		return resetLockedSettings(ctx, conn, s)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 拿到写锁后再确认一次：并发启动的另一个进程可能已经合并完了。
	if h, ok, err := readSeedHash(ctx, tx, DatasetModelCenter); err != nil {
		return err
	} else if ok && h == s.Hash {
		return nil
	}
	if err := applyModels(ctx, tx, s); err != nil {
		return fmt.Errorf("合并内置模型: %w", err)
	}
	// 配置在模型之后合并：配置里引用的模型要按合并后的模型集合校验。
	if err := applySettings(ctx, tx, s); err != nil {
		return fmt.Errorf("合并内置配置: %w", err)
	}
	if err := writeSeedHash(ctx, tx, DatasetModelCenter, s.Hash); err != nil {
		return err
	}
	return tx.Commit()
}

// applyModels 按 model_id 合并模型。
func applyModels(ctx context.Context, q querier, s *Seed) error {
	cur, err := loadModelRows(ctx, q)
	if err != nil {
		return err
	}
	// 1. 新版种子里没有的模型删除。模型全部内置、用户不能新增，所以库里不存在需要保留的“用户模型”。
	for id := range cur {
		if _, ok := s.Model(id); !ok {
			// 独立上游是本机用户数据，随行删除（契约）；打一条日志，升级后"独立上游"标签消失、
			// 请求改走全局时有据可查。只记地址，不记 Key。
			var upBase string
			if err := q.QueryRowContext(ctx, `SELECT upstream_base_url FROM model_configs WHERE model_id = ?`, id).Scan(&upBase); err == nil && upBase != "" {
				log.Printf("builtin: 新版种子移除了模型 %s，它单独配置的独立上游（%s）随之删除，之后改走全局上游", id, upBase)
			}
			if err := deleteModelRow(ctx, q, id); err != nil {
				return err
			}
			delete(cur, id)
		}
	}
	// 2. 主键与种子不一致的行先挪到负数临时主键，第 3 步再写成种子 id，避免逐行改主键时撞上别的行。
	//    种子保留生产 id 是因为 agent_tools.source_model_id 引用它。
	for _, m := range s.Models {
		if r, ok := cur[m.ModelID]; ok && r.ID != m.ID {
			if err := moveModelID(ctx, q, r.ID, -r.ID); err != nil {
				return err
			}
			r.ID = -r.ID
			cur[m.ModelID] = r
		}
	}
	cat := s.Catalog()
	// 3. 新模型插入；已有模型以新版种子为底，盖上用户改过的字段。
	for _, m := range s.Models {
		seedRow := rowFromSeed(m)
		r, ok := cur[m.ModelID]
		if !ok {
			if err := insertModelRow(ctx, q, seedRow); err != nil {
				return fmt.Errorf("插入 %s: %w", m.ModelID, err)
			}
			continue
		}
		merged, err := applyMergeModel(seedRow, r, cat)
		if err != nil {
			return fmt.Errorf("合并 %s: %w", m.ModelID, err)
		}
		if err := updateModelRow(ctx, q, merged); err != nil {
			return fmt.Errorf("更新 %s: %w", m.ModelID, err)
		}
	}
	return nil
}

func applyMergeModel(seedRow, cur modelRow, cat Catalog) (modelRow, error) {
	if len(cur.Overrides) == 0 {
		return seedRow, nil
	}
	base, err := modelDoc(seedRow.Parts)
	if err != nil {
		return modelRow{}, err
	}
	user, err := modelDoc(cur.Parts)
	if err != nil {
		// 库里的值坏了就放弃用户修改，回到内置值，不能让启动失败。
		log.Printf("内置数据合并：模型 %s 的当前值无法解析（%v），丢弃用户修改 %v", cur.ModelID, err, cur.Overrides)
		return seedRow, nil
	}
	doc, kept, dropped := overlay(modelPolicy, base, user, cur.Overrides, cat)
	if len(dropped) > 0 {
		log.Printf("内置数据合并：模型 %s 的用户修改 %v 在新版中已失效，改为跟随内置值", cur.ModelID, dropped)
	}
	parts, err := partsFromDoc(doc, seedRow.Parts, kept)
	if err != nil {
		return modelRow{}, err
	}
	out := seedRow
	out.Parts = parts
	out.Overrides = kept
	return out, nil
}

// applySettings 合并种子里的配置 key。不在种子里的 key（upstream_api_key 等）一概不碰；
// 旧版种子有、新版去掉的 key 也保留不删——无法区分它来自种子还是运行时写入，留着不生效也无害。
func applySettings(ctx context.Context, q querier, s *Seed) error {
	cat, err := loadCatalog(ctx, q)
	if err != nil {
		return err
	}
	for _, st := range s.Settings {
		pl, ok := settingPolicies[st.Key]
		if !ok {
			return fmt.Errorf("种子配置 %s 没有对应的可改字段策略", st.Key)
		}
		cur, found, err := loadSettingRow(ctx, q, st.Key)
		if err != nil {
			return err
		}
		row := settingRow{Key: st.Key, Value: st.Value}
		if found && len(cur.Overrides) > 0 {
			row, err = applyMergeSetting(pl, st, cur, cat)
			if err != nil {
				return fmt.Errorf("合并 %s: %w", st.Key, err)
			}
		}
		if err := upsertSettingRow(ctx, q, row); err != nil {
			return err
		}
	}
	return nil
}

func applyMergeSetting(pl policy, st SeedSetting, cur settingRow, cat Catalog) (settingRow, error) {
	base, err := settingDoc(pl.kind, st.Value)
	if err != nil {
		return settingRow{}, fmt.Errorf("种子值无法解析: %w", err)
	}
	user, err := settingDoc(pl.kind, cur.Value)
	if err != nil {
		log.Printf("内置数据合并：配置 %s 的当前值无法解析（%v），丢弃用户修改 %v", st.Key, err, cur.Overrides)
		return settingRow{Key: st.Key, Value: st.Value}, nil
	}
	doc, kept, dropped := overlay(pl, base, user, cur.Overrides, cat)
	if len(dropped) > 0 {
		log.Printf("内置数据合并：配置 %s 的用户修改 %v 在新版中已失效，改为跟随内置值", st.Key, dropped)
	}
	v, err := settingValueFromDoc(pl.kind, st.Value, doc, kept)
	if err != nil {
		return settingRow{}, err
	}
	return settingRow{Key: st.Key, Value: v, Overrides: kept}, nil
}

// resetLockedSettings 把写死的配置（白名单为空，见 policy.go）里残留的用户修改清掉、恢复为种子值。
// 种子没变的启动也要跑：库可能是写死之前的版本留下的。只碰这几项配置，不碰模型和 base_domain。
// 正常情况下只有几次只读查询；需要改时才写库。
func resetLockedSettings(ctx context.Context, conn *sql.DB, s *Seed) error {
	for _, st := range s.Settings {
		pl, ok := settingPolicies[st.Key]
		if !ok || !pl.locked() {
			continue
		}
		cur, found, err := loadSettingRow(ctx, conn, st.Key)
		if err != nil {
			return err
		}
		if !found || (len(cur.Overrides) == 0 && cur.Value == st.Value) {
			continue
		}
		log.Printf("内置数据：配置 %s 已写死为内置值，丢弃用户修改 %v", st.Key, cur.Overrides)
		if err := upsertSettingRow(ctx, conn, settingRow{Key: st.Key, Value: st.Value}); err != nil {
			return err
		}
	}
	return nil
}
