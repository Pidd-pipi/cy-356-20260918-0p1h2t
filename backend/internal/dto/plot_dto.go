package dto

import (
	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
)

// CreatePlotRequest 创建地块（管理员）。
type CreatePlotRequest struct {
	Name        string  `json:"name" binding:"required,max=128"`
	Code        string  `json:"code" binding:"required,max=32"`
	Area        float64 `json:"area" binding:"required,gt=0"`
	SoilType    string  `json:"soil_type" binding:"required,oneof=loam clay sand black"`
	Sunlight    string  `json:"sunlight" binding:"required,oneof=full partial shade"`
	Latitude    float64 `json:"latitude" binding:"required,min=-90,max=90"`
	Longitude   float64 `json:"longitude" binding:"required,min=-180,max=180"`
	Description string  `json:"description" binding:"omitempty,max=512"`
}

// UpdatePlotRequest 更新地块（管理员）。
type UpdatePlotRequest struct {
	Name        *string  `json:"name" binding:"omitempty,max=128"`
	Code        *string  `json:"code" binding:"omitempty,max=32"`
	Area        *float64 `json:"area" binding:"omitempty,gt=0"`
	SoilType    *string  `json:"soil_type" binding:"omitempty,oneof=loam clay sand black"`
	Sunlight    *string  `json:"sunlight" binding:"omitempty,oneof=full partial shade"`
	Latitude    *float64 `json:"latitude" binding:"omitempty,min=-90,max=90"`
	Longitude   *float64 `json:"longitude" binding:"omitempty,min=-180,max=180"`
	Description *string  `json:"description" binding:"omitempty,max=512"`
}

// PlotOutDTO 地块输出。
type PlotOutDTO struct {
	ID          uint        `json:"id"`
	Name        string      `json:"name"`
	Code        string      `json:"code"`
	Area        float64     `json:"area"`
	SoilType    string      `json:"soil_type"`
	Sunlight    string      `json:"sunlight"`
	Latitude    float64     `json:"latitude"`
	Longitude   float64     `json:"longitude"`
	Status      string      `json:"status"`
	AdopterID   *uint       `json:"adopter_id"`
	Adopter     *UserOutDTO `json:"adopter"`
	Description string      `json:"description"`
	CreatedAt   string      `json:"created_at"`
	// 释放入口前置条件：对应种植计划已完成 且 至少一条收成记录。
	HasCompletedPlan   bool   `json:"has_completed_plan"`
	HasHarvestRecord   bool   `json:"has_harvest_record"`
	CanRelease         bool   `json:"can_release"`
	ReleaseBlockReason string `json:"release_block_reason"`
}

// ToPlotOutDTO 模型转 DTO（不含释放入口字段，需调用方用 FillReleaseReadiness 填充）。
func ToPlotOutDTO(p *model.Plot) *PlotOutDTO {
	dto := &PlotOutDTO{
		ID:          p.ID,
		Name:        p.Name,
		Code:        p.Code,
		Area:        p.Area,
		SoilType:    p.SoilType,
		Sunlight:    p.Sunlight,
		Latitude:    p.Latitude,
		Longitude:   p.Longitude,
		Status:      p.Status,
		AdopterID:   p.AdopterID,
		Description: p.Description,
		CreatedAt:   p.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if p.Adopter != nil {
		dto.Adopter = ToUserOutDTO(p.Adopter)
	}
	return dto
}

// PlotReleaseReadinessView 释放入口前置条件视图（service 层计算结果）。
type PlotReleaseReadinessView struct {
	HasCompletedPlan bool
	HasHarvestRecord bool
}

// Releaseable 两项条件同时满足入口才可用。
func (r PlotReleaseReadinessView) Releaseable() bool {
	return r.HasCompletedPlan && r.HasHarvestRecord
}

// FillReleaseReadiness 填充释放入口显隐与缺失项说明（仅对已认养地块给出原因）。
func FillReleaseReadiness(out *PlotOutDTO, r PlotReleaseReadinessView) {
	out.HasCompletedPlan = r.HasCompletedPlan
	out.HasHarvestRecord = r.HasHarvestRecord
	out.CanRelease = r.Releaseable()
	if out.AdopterID == nil || r.Releaseable() {
		out.ReleaseBlockReason = ""
		return
	}
	switch {
	case !r.HasCompletedPlan && !r.HasHarvestRecord:
		out.ReleaseBlockReason = constants.MsgReleaseNeedBoth
	case !r.HasCompletedPlan:
		out.ReleaseBlockReason = constants.MsgReleaseNeedPlan
	default:
		out.ReleaseBlockReason = constants.MsgReleaseNeedHarvest
	}
}
