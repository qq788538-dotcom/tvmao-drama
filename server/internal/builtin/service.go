package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// Service 是设置页修改内置数据的唯一入口：只放行白名单字段、保存前校验、
// 记录用户改过的字段、提供“恢复默认”。内置模型和内置配置不得再经
// models.Repository 的 Create/Update/Delete 或 settings.Repository.Set 写入，否则覆盖记录会失真。
type Service struct {
	db   *sql.DB
	seed *Seed
}

// NewService 用编译进二进制的种子创建 Service。调用前 Apply 必须已经成功。
func NewService(conn *sql.DB) (*Service, error) {
	s, err := Load()
	if err != nil {
		return nil, err
	}
	return &Service{db: conn, seed: s}, nil
}

// ModelState 是一个内置模型的当前值和用户改过的字段。
type ModelState struct {
	models.ModelConfig
	Overrides []string `json:"overrides"`
}

// SettingState 是一项内置配置的当前值（原文）和用户改过的字段。
type SettingState struct {
	Key       string   `json:"key"`
	Value     string   `json:"value"`
	Overrides []string `json:"overrides"`
}

func nonNil(o []string) []string {
	if o == nil {
		return []string{}
	}
	return o
}

// ListModels 返回全部内置模型（含隐藏、停用的），顺序同 models.Repository.List。
func (s *Service) ListModels(ctx context.Context) ([]ModelState, error) {
	list, err := models.NewSQLiteRepository(s.db).List(false)
	if err != nil {
		return nil, err
	}
	rows, err := loadModelRows(ctx, s.db)
	if err != nil {
		return nil, err
	}
	out := make([]ModelState, 0, len(list))
	for _, m := range list {
		out = append(out, ModelState{ModelConfig: m, Overrides: nonNil(rows[m.ModelID].Overrides)})
	}
	return out, nil
}

func (s *Service) modelState(ctx context.Context, modelID string) (ModelState, error) {
	m, err := models.NewSQLiteRepository(s.db).GetByModelID(modelID)
	if err != nil {
		return ModelState{}, err
	}
	r, err := loadModelRow(ctx, s.db, modelID)
	if err != nil {
		return ModelState{}, err
	}
	return ModelState{ModelConfig: *m, Overrides: nonNil(r.Overrides)}, nil
}

// UpdateModel 修改一个内置模型。edits 的 key 是字段路径（见 ModelEditableFields），value 是新值的 JSON。
// 任何一项不在白名单或校验不通过，返回 *ValidationError，库不变。
func (s *Service) UpdateModel(ctx context.Context, modelID string, edits map[string]json.RawMessage) (ModelState, error) {
	sm, ok := s.seed.Model(modelID)
	if !ok {
		return ModelState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelState{}, err
	}
	defer tx.Rollback()
	cur, err := loadModelRow(ctx, tx, modelID)
	if err != nil {
		return ModelState{}, err
	}
	cat, err := loadCatalog(ctx, tx)
	if err != nil {
		return ModelState{}, err
	}
	seedParts := sm.parts()
	curDoc, err := modelDoc(cur.Parts)
	if err != nil {
		return ModelState{}, err
	}
	seedDoc, err := modelDoc(seedParts)
	if err != nil {
		return ModelState{}, err
	}
	doc, ov, err := applyEdits(modelPolicy, curDoc, seedDoc, cur.Overrides, edits, cat)
	if err != nil {
		return ModelState{}, err
	}
	if cur.Parts, err = partsFromDoc(doc, seedParts, ov); err != nil {
		return ModelState{}, err
	}
	cur.Overrides = ov
	if err := updateModelRow(ctx, tx, cur); err != nil {
		return ModelState{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelState{}, err
	}
	return s.modelState(ctx, modelID)
}

// RestoreModelDefault 把一个内置模型整行恢复成种子值并清空覆盖记录（设置页的“恢复默认”）。
func (s *Service) RestoreModelDefault(ctx context.Context, modelID string) (ModelState, error) {
	sm, ok := s.seed.Model(modelID)
	if !ok {
		return ModelState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelState{}, err
	}
	defer tx.Rollback()
	if _, err := loadModelRow(ctx, tx, modelID); err != nil {
		return ModelState{}, err
	}
	if err := updateModelRow(ctx, tx, rowFromSeed(sm)); err != nil {
		return ModelState{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelState{}, err
	}
	return s.modelState(ctx, modelID)
}

// ListSettings 返回全部内置配置（种子里的 key，按种子顺序）。不含 upstream_api_key 等非内置 key。
func (s *Service) ListSettings(ctx context.Context) ([]SettingState, error) {
	out := make([]SettingState, 0, len(s.seed.Settings))
	for _, st := range s.seed.Settings {
		state, err := s.settingState(ctx, s.db, st.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

func (s *Service) settingState(ctx context.Context, q querier, key string) (SettingState, error) {
	r, found, err := loadSettingRow(ctx, q, key)
	if err != nil {
		return SettingState{}, err
	}
	if !found {
		return SettingState{}, fmt.Errorf("内置配置 %s 不在库里，Apply 没有执行？", key)
	}
	return SettingState{Key: key, Value: r.Value, Overrides: nonNil(r.Overrides)}, nil
}

// UpdateSetting 修改一项内置配置。edits 的 key 是字段路径（见 SettingEditableFields），
// 纯文本配置（base_domain）用 "$" 表示整个值。
func (s *Service) UpdateSetting(ctx context.Context, key string, edits map[string]json.RawMessage) (SettingState, error) {
	st, ok := s.seed.Setting(key)
	pl, hasPolicy := settingPolicies[key]
	if !ok || !hasPolicy {
		return SettingState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SettingState{}, err
	}
	defer tx.Rollback()
	cur, found, err := loadSettingRow(ctx, tx, key)
	if err != nil {
		return SettingState{}, err
	}
	if !found {
		cur = settingRow{Key: key, Value: st.Value}
	}
	cat, err := loadCatalog(ctx, tx)
	if err != nil {
		return SettingState{}, err
	}
	curDoc, err := settingDoc(pl.kind, cur.Value)
	if err != nil {
		return SettingState{}, fmt.Errorf("配置 %s 的当前值无法解析: %w", key, err)
	}
	seedDoc, err := settingDoc(pl.kind, st.Value)
	if err != nil {
		return SettingState{}, err
	}
	doc, ov, err := applyEdits(pl, curDoc, seedDoc, cur.Overrides, edits, cat)
	if err != nil {
		return SettingState{}, err
	}
	v, err := settingValueFromDoc(pl.kind, st.Value, doc, ov)
	if err != nil {
		return SettingState{}, err
	}
	if err := upsertSettingRow(ctx, tx, settingRow{Key: key, Value: v, Overrides: ov}); err != nil {
		return SettingState{}, err
	}
	if err := tx.Commit(); err != nil {
		return SettingState{}, err
	}
	return s.settingState(ctx, s.db, key)
}

// RestoreSettingDefault 把一项内置配置恢复成种子值并清空覆盖记录。
func (s *Service) RestoreSettingDefault(ctx context.Context, key string) (SettingState, error) {
	st, ok := s.seed.Setting(key)
	if !ok {
		return SettingState{}, ErrNotFound
	}
	if err := upsertSettingRow(ctx, s.db, settingRow{Key: key, Value: st.Value}); err != nil {
		return SettingState{}, err
	}
	return s.settingState(ctx, s.db, key)
}
