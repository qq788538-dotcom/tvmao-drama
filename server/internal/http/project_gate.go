package http

import (
	"context"
	"errors"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

// projectGate 校验项目存在且未软删（*projects.SQLiteRepository 实现）。
type projectGate interface {
	ActiveExists(ctx context.Context, projectID int64) (bool, error)
}

// errProjectGone 是提交入口发现项目不存在或已删除时的错误，文案可直接给用户看。
var errProjectGone = errors.New(projects.GoneMessage)

// checkProjectActive 是提交入口（canvasgen 提交、资产上传）共用的存在性校验，做法对照
// cliapi.RunNode：往已删项目里建任务/资产，产物会落到没人清理的项目上。
// gate 为 nil（未接线的单测）或 projectID=0（不挂项目）时跳过。
func checkProjectActive(ctx context.Context, gate projectGate, projectID int64) error {
	if gate == nil || projectID == 0 {
		return nil
	}
	ok, err := gate.ActiveExists(ctx, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errProjectGone
	}
	return nil
}
