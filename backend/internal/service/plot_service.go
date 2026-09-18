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

// PlotService 地块服务（认养使用事务 + SELECT FOR UPDATE）。
type PlotService struct {
	plotRepo    repository.PlotRepository
	planRepo    repository.PlantingPlanRepository
	harvestRepo repository.HarvestRecordRepository
	db          *gorm.DB
	logger      *slog.Logger
}

// NewPlotService 构造地块服务。
func NewPlotService(plotRepo repository.PlotRepository, planRepo repository.PlantingPlanRepository, harvestRepo repository.HarvestRecordRepository, db *gorm.DB, logger *slog.Logger) *PlotService {
	return &PlotService{plotRepo: plotRepo, planRepo: planRepo, harvestRepo: harvestRepo, db: db, logger: logger}
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

// evaluateReleaseEligibility 在事务内、地块行锁保护下评估释放条件。
// 规则：地块须处于认养中，且存在至少一条“已完成”的种植计划，
// 并且该地块下至少有一条收成记录关联到“已完成”的种植计划。
// 仅有完成计划但无收成、或收成对应的计划未完成，均不可释放。
func (s *PlotService) evaluateReleaseEligibility(tx *gorm.DB, plot *model.Plot) (*dto.ReleaseEligibility, error) {
	result := &dto.ReleaseEligibility{}
	if plot.Status == string(constants.PlotStatusAvailable) || plot.AdopterID == nil {
		result.ReasonCode = string(constants.ReleaseBlockerNotAdopted)
		result.Reason = util.PlotReleaseReasonText(result.ReasonCode)
		return result, nil
	}
	hasPlan, err := s.planRepo.ExistsByPlotTx(tx, plot.ID)
	if err != nil {
		return nil, err
	}
	result.HasPlan = hasPlan
	if !hasPlan {
		result.ReasonCode = string(constants.ReleaseBlockerNoPlan)
		result.Reason = util.PlotReleaseReasonText(result.ReasonCode)
		return result, nil
	}
	completed, err := s.planRepo.CountCompletedByPlotTx(tx, plot.ID)
	if err != nil {
		return nil, err
	}
	result.CompletedPlans = completed
	if completed == 0 {
		result.ReasonCode = string(constants.ReleaseBlockerPlanOngoing)
		result.Reason = util.PlotReleaseReasonText(result.ReasonCode)
		return result, nil
	}
	harvests, err := s.harvestRepo.CountLinkedToCompletedPlanByPlotTx(tx, plot.ID)
	if err != nil {
		return nil, err
	}
	result.CompletedHarvests = harvests
	if harvests == 0 {
		// 进一步区分：是完全没有收成，还是收成只挂在未完成的计划上。
		var anyHarvest int64
		if err := tx.Model(&model.HarvestRecord{}).
			Joins("JOIN planting_plans ON planting_plans.id = harvest_records.plan_id").
			Where("planting_plans.plot_id = ?", plot.ID).
			Count(&anyHarvest).Error; err != nil {
			return nil, err
		}
		if anyHarvest > 0 {
			result.ReasonCode = string(constants.ReleaseBlockerHarvestOnUnfinishedPlan)
		} else {
			result.ReasonCode = string(constants.ReleaseBlockerNoHarvest)
		}
		result.Reason = util.PlotReleaseReasonText(result.ReasonCode)
		return result, nil
	}
	result.Releasable = true
	return result, nil
}

// ReleaseEligibility 查询地块是否满足释放条件（供前端释放入口感知，只读、不加行锁）。
// 这里是用于按钮显隐的“快照”判断；真正的权威判定在 Release 内以事务 + FOR UPDATE 重新执行。
func (s *PlotService) ReleaseEligibility(plotID uint) (*dto.ReleaseEligibility, error) {
	plot, err := s.plotRepo.FindByID(plotID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
		}
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	result, err := s.evaluateReleaseEligibility(s.db, plot)
	if err != nil {
		return nil, util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
	}
	s.logger.Info(constants.LogPlotReleaseEligibility, "plot_id", plotID, "releasable", result.Releasable, "reason", result.ReasonCode)
	return result, nil
}

// Release 释放地块（管理员或认养人）。
// 前置条件：存在已完成的种植计划且至少一条关联到已完成计划的收成记录。
// 整个判定与状态变更在同一事务 + 行锁内完成，并发释放只有一人成功，
// 失败方不会清除认养关系（adopter_id）也不会改变地块状态。
func (s *PlotService) Release(plotID, operatorID uint, operatorRole string) (*model.Plot, error) {
	var released *model.Plot
	var blocked *dto.ReleaseEligibility
	var blockedCode, blockedStatus string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, 404, fmt.Sprintf("地块实体 id=%d 不存在", plotID))
			}
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		// 并发释放：地块若已被另一操作者释放（available），直接判定状态冲突。
		// 该校验先于权限校验，确保失败方拿到 409 冲突，且绝不清理认养关系。
		if plot.Status == string(constants.PlotStatusAvailable) || plot.AdopterID == nil {
			return util.NewAppError(constants.CodePlotStateConflict, 409,
				fmt.Sprintf("地块 %s 当前状态为 %s，可能已被其他操作者释放，请刷新后重试", plot.Code, util.PlotStatusText(plot.Status)))
		}
		if operatorRole != string(constants.RoleAdmin) && *plot.AdopterID != operatorID {
			return util.NewAppError(constants.CodeForbidden, 403, fmt.Sprintf("角色 %s 无权释放地块 %s", util.RoleText(operatorRole), plot.Code))
		}
		eligibility, err := s.evaluateReleaseEligibility(tx, plot)
		if err != nil {
			return util.NewAppError(constants.CodeInternalError, 500, constants.ErrorText[constants.CodeInternalError]).Wrap(err)
		}
		if !eligibility.Releasable {
			blocked = eligibility
			blockedCode, blockedStatus = plot.Code, plot.Status
			return util.NewAppError(constants.CodePlotReleasePrecondition, 409,
				fmt.Sprintf("地块 %s 暂不能释放：%s", plot.Code, eligibility.Reason))
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
		if blocked != nil {
			s.logger.Info(constants.LogPlotReleaseBlocked, "plot_id", plotID, "code", blockedCode, "operator", operatorID, "role", operatorRole,
				"status", blockedStatus, "has_plan", blocked.HasPlan, "completed_plans", blocked.CompletedPlans,
				"completed_plan_harvests", blocked.CompletedHarvests, "reason", blocked.ReasonCode)
		}
		return nil, err
	}
	s.logger.Info(constants.LogPlotReleased, "plot_id", released.ID, "code", released.Code, "operator", operatorID)
	return released, nil
}

// RefreshHarvestReadyTx 在收成/计划变更后，按“已完成计划 + 关联收成”规则重新校准地块状态。
// 满足条件时 adopted -> harvested（出现释放入口）；不再满足时 harvested -> adopted（撤回入口）。
// 幂等；释放后（available）不做处理。必须在调用方事务内执行。
func (s *PlotService) RefreshHarvestReadyTx(tx *gorm.DB, plotID uint, trigger string) error {
	plot, err := s.plotRepo.FindByIDForUpdate(tx, plotID)
	if err != nil {
		return err
	}
	if plot.Status == string(constants.PlotStatusAvailable) {
		return nil
	}
	eligibility, err := s.evaluateReleaseEligibility(tx, plot)
	if err != nil {
		return err
	}
	target := plot.Status
	if eligibility.Releasable {
		target = string(constants.PlotStatusHarvested)
	} else {
		target = string(constants.PlotStatusAdopted)
	}
	if target != plot.Status {
		plot.Status = target
		if err := s.plotRepo.UpdateWithTx(tx, plot); err != nil {
			return err
		}
		s.logger.Info(constants.LogPlotHarvestReady, "plot_id", plotID, "trigger", trigger, "plan_id", 0)
	}
	return nil
}

// CountByStatus 地块状态统计（仪表盘复用）。
func (s *PlotService) CountByStatus() (map[string]int64, error) {
	return s.plotRepo.CountByStatus()
}
