package service

import (
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/dto"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

// PlotService 地块服务（认养/释放使用事务 + SELECT FOR UPDATE）。
type PlotService struct {
	plotRepo    repository.PlotRepository
	planRepo    repository.PlantingPlanRepository
	harvestRepo repository.HarvestRecordRepository
	db          *gorm.DB
	logger      *slog.Logger
}

// ReleaseReadiness 地块释放入口前置条件（两项必须同时满足）。
type ReleaseReadiness struct {
	HasCompletedPlan bool // 对应种植计划已完成
	HasHarvestRecord bool // 已完成计划至少有一条收成记录
}

// Releaseable 两项前置条件同时满足时入口才可用。
func (r ReleaseReadiness) Releaseable() bool {
	return r.HasCompletedPlan && r.HasHarvestRecord
}

// NewPlotService 构造地块服务。
func NewPlotService(plotRepo repository.PlotRepository, planRepo repository.PlantingPlanRepository, harvestRepo repository.HarvestRecordRepository, db *gorm.DB, logger *slog.Logger) *PlotService {
	return &PlotService{plotRepo: plotRepo, planRepo: planRepo, harvestRepo: harvestRepo, db: db, logger: logger}
}

// releaseBlockReason 按缺失项拼装释放入口不可用原因（说明缺少哪一项）。
func releaseBlockReason(code string, r ReleaseReadiness) string {
	switch {
	case !r.HasCompletedPlan && !r.HasHarvestRecord:
		return fmt.Sprintf("地块 %s：%s", code, constants.MsgReleaseNeedBoth)
	case !r.HasCompletedPlan:
		return fmt.Sprintf("地块 %s：%s", code, constants.MsgReleaseNeedPlan)
	default:
		return fmt.Sprintf("地块 %s：%s", code, constants.MsgReleaseNeedHarvest)
	}
}

// GetByID 查询地块详情（被地块 handler 与种植计划 service 复用）。
func (s *PlotService) GetByID(id uint) (*model.Plot, error) {
	p, err := s.plotRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", id))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return p, nil
}

// Create 创建地块（管理员）。
func (s *PlotService) Create(req *dto.CreatePlotRequest, operator string) (*model.Plot, error) {
	if _, err := s.plotRepo.FindByCode(req.Code); err == nil {
		return nil, util.NewAppError(constants.CodeConflict, 409, fmt.Sprintf("地块编号 %s 已存在", req.Code))
	}
	p := &model.Plot{
		Name:        req.Name,
		Code:        req.Code,
		Area:        req.Area,
		SoilType:    req.SoilType,
		Sunlight:    req.Sunlight,
		Latitude:    req.Latitude,
		Longitude:   req.Longitude,
		Status:      string(constants.PlotStatusAvailable),
		Description: req.Description,
	}
	if err := s.plotRepo.Create(p); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	s.logger.Info(constants.LogPlotCreated, "plot_id", p.ID, "code", p.Code, "operator", operator)
	return p, nil
}

// Update 更新地块（管理员）。
func (s *PlotService) Update(id uint, req *dto.UpdatePlotRequest, operator string) (*model.Plot, error) {
	p, err := s.plotRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", id))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Code != nil {
		p.Code = *req.Code
	}
	if req.Area != nil {
		p.Area = *req.Area
	}
	if req.SoilType != nil {
		p.SoilType = *req.SoilType
	}
	if req.Sunlight != nil {
		p.Sunlight = *req.Sunlight
	}
	if req.Latitude != nil {
		p.Latitude = *req.Latitude
	}
	if req.Longitude != nil {
		p.Longitude = *req.Longitude
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if err := s.plotRepo.Update(p); err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return p, nil
}

// List 分页查询地块（可过滤状态）。
func (s *PlotService) List(pq util.PageQuery, status string) ([]model.Plot, int64, error) {
	plots, total, err := s.plotRepo.List(pq, status)
	if err != nil {
		return nil, 0, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return plots, total, nil
}

// Adopt 认养地块（事务 + 行锁，available -> adopted）。
func (s *PlotService) Adopt(plotID, userID uint, role, username string) (*model.Plot, error) {
	var adopted *model.Plot
	err := s.db.Transaction(func(tx *gorm.DB) error {
		plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if plot.Status != string(constants.PlotStatusAvailable) {
			return util.NewAppError(constants.CodePlotNotAvailable, 409, fmt.Sprintf("地块 %s 当前状态为 %s，不可认养", plot.Code, util.PlotStatusText(plot.Status)))
		}
		plot.Status = string(constants.PlotStatusAdopted)
		plot.AdopterID = &userID
		if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		adopted = plot
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogPlotAdopted, "plot_id", adopted.ID, "code", adopted.Code, "user_id", userID, "role", role)
	return adopted, nil
}

// Release 释放地块（管理员或认养人）。
// 释放入口前置条件：对应种植计划已完成 且 至少有一条对应收成记录；二者缺一不可。
// 两名操作者并发释放同一地块时，依赖 SELECT ... FOR UPDATE 行锁串行化：
// 仅第一个事务能把 harvested -> available 并清空认养关系，第二个事务复核时
// 地块已是 available，直接返回冲突错误，不会清空认养关系或改变状态。
func (s *PlotService) Release(plotID, operatorID uint, operatorRole string) (*model.Plot, error) {
	var released *model.Plot
	var blockedReason string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 1. 行锁锁定地块（并发释放在此串行）
		plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		// 2. 权限校验：管理员或当前认养人
		if operatorRole != string(constants.RoleAdmin) && (plot.AdopterID == nil || *plot.AdopterID != operatorID) {
			return util.NewAppError(constants.CodeForbidden, 403, fmt.Sprintf("角色 %s 无权释放地块 %s", util.RoleText(operatorRole), plot.Code))
		}
		// 3. 空闲地块不可释放：并发场景下失败方在此被拦截（成功者已清空认养关系、状态置 available）
		if plot.Status == string(constants.PlotStatusAvailable) || plot.AdopterID == nil {
			return util.NewAppError(constants.CodePlotReleaseConflict, 409, fmt.Sprintf("地块 %s 已被释放（%s），请勿重复操作", plot.Code, util.PlotStatusText(plot.Status)))
		}
		// 4. 行锁后复核前置条件（锁内最新数据，条件不足时不做任何变更）。
		// 双条件是唯一释放闸门，地块状态字段仅作入口展示，不再作为附加限制。
		readiness, err := s.releaseReadiness(tx, plot)
		if err != nil {
			return err
		}
		if !readiness.Releaseable() {
			blockedReason = releaseBlockReason(plot.Code, readiness)
			return util.NewAppError(constants.CodeReleasePrerequisite, 409, blockedReason)
		}
		plot.Status = string(constants.PlotStatusAvailable)
		plot.AdopterID = nil
		if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		released = plot
		return nil
	})
	if err != nil {
		if blockedReason != "" {
			s.logger.Warn(constants.LogPlotReleaseBlocked, "plot_id", plotID, "operator", operatorID, "reason", blockedReason)
		}
		return nil, err
	}
	s.logger.Info(constants.LogPlotReleased, "plot_id", released.ID, "code", released.Code, "operator", operatorID)
	return released, nil
}

// releaseReadiness 在指定事务内计算释放入口前置条件（tx 为 nil 时走普通读）。
// 认养关系以地块当前认养人为准（仅统计当前认养周期的计划/收成）。
func (s *PlotService) releaseReadiness(tx *gorm.DB, plot *model.Plot) (ReleaseReadiness, error) {
	if plot.AdopterID == nil {
		return ReleaseReadiness{}, nil
	}
	ids := []uint{plot.ID}
	adopterID := *plot.AdopterID
	completed, err := s.planRepo.CompletedPlotIDs(tx, ids, adopterID)
	if err != nil {
		return ReleaseReadiness{}, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	harvested, err := s.harvestRepo.PlotIDsWithHarvest(tx, ids, adopterID)
	if err != nil {
		return ReleaseReadiness{}, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	return ReleaseReadiness{HasCompletedPlan: completed[plot.ID], HasHarvestRecord: harvested[plot.ID]}, nil
}

// RecomputeReleaseStatus 依据前置条件重算地块状态（事务内调用，调用方持地块行锁）：
// 计划已完成且至少一条对应收成记录 -> harvested（待释放）；否则保持 adopted。
// 触发点：种植计划流转 completed、收成记录创建/删除。
// 规则覆盖：有完成计划但没有收成记录、收成记录对应计划未完成，均保持已认养。
func (s *PlotService) RecomputeReleaseStatus(tx *gorm.DB, plotID uint) error {
	plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
		}
		return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	if plot.Status == string(constants.PlotStatusAvailable) || plot.AdopterID == nil {
		return nil
	}
	readiness, err := s.releaseReadiness(tx, plot)
	if err != nil {
		return err
	}
	target := string(constants.PlotStatusAdopted)
	if readiness.Releaseable() {
		target = string(constants.PlotStatusHarvested)
	}
	if plot.Status != target {
		plot.Status = target
		if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		s.logger.Info(constants.LogPlotEligibilityChanged, "plot_id", plot.ID, "code", plot.Code, "status", target,
			"plan_completed", readiness.HasCompletedPlan, "has_harvest", readiness.HasHarvestRecord)
	}
	return nil
}

// EnrichReadiness 批量填充地块的释放入口前置条件与缺失原因（供列表/详情 DTO 使用）。
func (s *PlotService) EnrichReadiness(plots []*model.Plot) (map[uint]ReleaseReadiness, error) {
	out := make(map[uint]ReleaseReadiness, len(plots))
	groups := make(map[uint][]uint)
	for _, p := range plots {
		if p.AdopterID == nil {
			out[p.ID] = ReleaseReadiness{}
			continue
		}
		aid := *p.AdopterID
		groups[aid] = append(groups[aid], p.ID)
	}
	for adopterID, ids := range groups {
		completed, err := s.planRepo.CompletedPlotIDs(nil, ids, adopterID)
		if err != nil {
			return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		harvested, err := s.harvestRepo.PlotIDsWithHarvest(nil, ids, adopterID)
		if err != nil {
			return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		for _, id := range ids {
			r := ReleaseReadiness{HasCompletedPlan: completed[id], HasHarvestRecord: harvested[id]}
			out[id] = r
		}
	}
	return out, nil
}

// CountByStatus 地块状态统计（仪表盘复用）。
func (s *PlotService) CountByStatus() (map[string]int64, error) {
	return s.plotRepo.CountByStatus()
}
