package repository

import (
	"testing"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/model"
)

// TestReleasePreconditionQueries 验证释放前置条件的事务内统计查询：
// 已完成计划数、任意计划是否存在、关联到已完成计划的收成数。
func TestReleasePreconditionQueries(t *testing.T) {
	db := newTestDB(t)
	plotRepo := NewPlotRepository(db)
	planRepo := NewPlantingPlanRepository(db)
	harvestRepo := NewHarvestRecordRepository(db)
	user := seedUser(t, db, "owner", "farmer")
	uid := user.ID

	// 两个地块：A 有完成计划+关联收成；B 有完成计划但收成挂在另一条未完成计划上；C 完全没有计划。
	plotA := &model.Plot{Name: "A", Code: "PA", Area: 10, SoilType: "loam", Sunlight: "full", Latitude: 31, Longitude: 121, Status: "harvested", AdopterID: &uid}
	plotB := &model.Plot{Name: "B", Code: "PB", Area: 10, SoilType: "loam", Sunlight: "full", Latitude: 31, Longitude: 121, Status: "adopted", AdopterID: &uid}
	plotC := &model.Plot{Name: "C", Code: "PC", Area: 10, SoilType: "loam", Sunlight: "full", Latitude: 31, Longitude: 121, Status: "adopted", AdopterID: &uid}
	for _, p := range []*model.Plot{plotA, plotB, plotC} {
		if err := plotRepo.Create(p); err != nil {
			t.Fatalf("create plot: %v", err)
		}
	}

	completedA := &model.PlantingPlan{PlotID: plotA.ID, UserID: uid, CropName: "番茄", CropType: "vegetable", Season: "summer", Status: "completed"}
	completedB := &model.PlantingPlan{PlotID: plotB.ID, UserID: uid, CropName: "番茄", CropType: "vegetable", Season: "summer", Status: "completed"}
	ongoingB := &model.PlantingPlan{PlotID: plotB.ID, UserID: uid, CropName: "生菜", CropType: "vegetable", Season: "spring", Status: "growing"}
	for _, p := range []*model.PlantingPlan{completedA, completedB, ongoingB} {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create plan: %v", err)
		}
	}
	hA := &model.HarvestRecord{PlanID: completedA.ID, UserID: uid, CropName: "番茄", WeightKg: 2, Quality: "good"}
	hB := &model.HarvestRecord{PlanID: ongoingB.ID, UserID: uid, CropName: "生菜", WeightKg: 1, Quality: "good"}
	for _, h := range []*model.HarvestRecord{hA, hB} {
		if err := db.Create(h).Error; err != nil {
			t.Fatalf("create harvest: %v", err)
		}
	}

	// 使用真实事务执行统计。
	if err := db.Transaction(func(tx *gorm.DB) error {
		existsA, err := planRepo.ExistsByPlotTx(tx, plotA.ID)
		if err != nil {
			return err
		}
		if !existsA {
			t.Errorf("plotA expected has plan")
		}
		completedCountA, err := planRepo.CountCompletedByPlotTx(tx, plotA.ID)
		if err != nil {
			return err
		}
		if completedCountA != 1 {
			t.Errorf("plotA completed=%d want 1", completedCountA)
		}
		linkedA, err := harvestRepo.CountLinkedToCompletedPlanByPlotTx(tx, plotA.ID)
		if err != nil {
			return err
		}
		if linkedA != 1 {
			t.Errorf("plotA linkedHarvest=%d want 1", linkedA)
		}

		// plotB：有完成计划，但收成只挂在未完成计划上，关联已完成计划的收成应为 0。
		completedCountB, err := planRepo.CountCompletedByPlotTx(tx, plotB.ID)
		if err != nil {
			return err
		}
		if completedCountB != 1 {
			t.Errorf("plotB completed=%d want 1", completedCountB)
		}
		linkedB, err := harvestRepo.CountLinkedToCompletedPlanByPlotTx(tx, plotB.ID)
		if err != nil {
			return err
		}
		if linkedB != 0 {
			t.Errorf("plotB linkedHarvest=%d want 0 (harvest only on unfinished plan)", linkedB)
		}

		// plotC：完全没有计划。
		existsC, err := planRepo.ExistsByPlotTx(tx, plotC.ID)
		if err != nil {
			return err
		}
		if existsC {
			t.Errorf("plotC expected no plan")
		}
		completedCountC, err := planRepo.CountCompletedByPlotTx(tx, plotC.ID)
		if err != nil {
			return err
		}
		if completedCountC != 0 {
			t.Errorf("plotC completed=%d want 0", completedCountC)
		}
		return nil
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}
}
