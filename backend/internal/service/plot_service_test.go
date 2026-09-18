package service

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/util"
)

// seedCompletedPlan 直接落库一条已完成种植计划。
func seedCompletedPlan(t *testing.T, db *gorm.DB, plotID, userID uint) *model.PlantingPlan {
	t.Helper()
	p := &model.PlantingPlan{
		PlotID: plotID, UserID: userID, CropName: "菠菜", CropType: "vegetable",
		Season: "spring", Status: string(constants.PlanStatusCompleted),
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return p
}

// seedHarvest 直接落库一条收成记录（可指定关联计划状态由调用方决定）。
func seedHarvest(t *testing.T, db *gorm.DB, planID, userID uint) *model.HarvestRecord {
	t.Helper()
	h := &model.HarvestRecord{
		PlanID: planID, UserID: userID, CropName: "菠菜",
		HarvestDate: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		WeightKg:    2.5, Quality: string(constants.QualityGood),
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("create harvest: %v", err)
	}
	return h
}

func TestPlotService_Adopt(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	user := newTestUser(t, db, "citizen", "citizen")
	plot := newTestPlot(t, db, "P-ADOPT", "available", nil)

	tests := []struct {
		name    string
		plotID  uint
		userID  uint
		wantErr bool
	}{
		{name: "adopt available", plotID: plot.ID, userID: user.ID, wantErr: false},
		{name: "adopt again conflicts", plotID: plot.ID, userID: user.ID, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.Adopt(tt.plotID, tt.userID, "citizen", "citizen")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			if got.Status != string(constants.PlotStatusAdopted) || got.AdopterID == nil || *got.AdopterID != tt.userID {
				t.Errorf("adopt result invalid: status=%s adopter=%v", got.Status, got.AdopterID)
			}
		})
	}
}

func TestPlotService_ReleasePrerequisites(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	owner := newTestUser(t, db, "farmer", "farmer")
	other := newTestUser(t, db, "citizen2", "citizen")
	uid := owner.ID

	// 场景 1：已认养但种植计划未完成 -> 释放入口不可用，地块保持 adopted
	plot1 := newTestPlot(t, db, "P-REL-1", "adopted", &uid)
	plan1 := seedCompletedPlan(t, db, plot1.ID, uid)
	// 计划存在但先置为未完成
	plan1.Status = string(constants.PlanStatusGrowing)
	if err := db.Save(plan1).Error; err != nil {
		t.Fatalf("update plan: %v", err)
	}
	seedHarvest(t, db, plan1.ID, uid) // 有收成记录但对应计划未完成
	if _, err := svc.Release(plot1.ID, owner.ID, "farmer"); err == nil {
		t.Fatalf("case1: expected release blocked (plan not completed)")
	}
	assertPlotUnchanged(t, db, plot1.ID, "adopted", uid)

	// 场景 2：计划已完成但没有任何收成记录 -> 保持 adopted
	plot2 := newTestPlot(t, db, "P-REL-2", "adopted", &uid)
	seedCompletedPlan(t, db, plot2.ID, uid)
	_, err := svc.Release(plot2.ID, owner.ID, "farmer")
	if err == nil {
		t.Fatalf("case2: expected release blocked (no harvest record)")
	}
	assertPlotUnchanged(t, db, plot2.ID, "adopted", uid)

	// 非认养人不能释放（失败方不能改变状态/认养关系）
	plot3 := newTestPlot(t, db, "P-REL-3", "adopted", &uid)
	plan3 := seedCompletedPlan(t, db, plot3.ID, uid)
	seedHarvest(t, db, plan3.ID, uid)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return svc.RecomputeReleaseStatus(tx, plot3.ID)
	}); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if _, err := svc.Release(plot3.ID, other.ID, "citizen"); err == nil {
		t.Fatalf("expected forbidden error for non-owner")
	}
	assertPlotUnchanged(t, db, plot3.ID, "harvested", uid)

	// 场景 3：两项条件都满足 -> 认养人可释放，状态 available 且认养关系清空
	got, err := svc.Release(plot3.ID, owner.ID, "farmer")
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got.Status != string(constants.PlotStatusAvailable) || got.AdopterID != nil {
		t.Errorf("release result invalid: status=%s adopter=%v", got.Status, got.AdopterID)
	}

	// 场景 4：重复释放（并发失败方路径）-> 冲突错误，地块保持 available
	if _, err := svc.Release(plot3.ID, owner.ID, "farmer"); err == nil {
		t.Fatalf("expected conflict on double release")
	}
}

func TestPlotService_ReleaseByAdmin(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	owner := newTestUser(t, db, "citizen", "citizen")
	uid := owner.ID
	plot := newTestPlot(t, db, "P-REL-ADMIN", "adopted", &uid)
	plan := seedCompletedPlan(t, db, plot.ID, uid)
	seedHarvest(t, db, plan.ID, uid)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return svc.RecomputeReleaseStatus(tx, plot.ID)
	}); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	adminID := uint(9999)
	got, err := svc.Release(plot.ID, adminID, string(constants.RoleAdmin))
	if err != nil {
		t.Fatalf("admin Release: %v", err)
	}
	if got.Status != "available" || got.AdopterID != nil {
		t.Errorf("admin release invalid: status=%s adopter=%v", got.Status, got.AdopterID)
	}
}

// TestPlotService_ConcurrentRelease 两名操作者并发释放同一地块：只允许一人成功，
// 失败方不能清除认养关系或改变状态。
func TestPlotService_ConcurrentRelease(t *testing.T) {
	concDSN := fmt.Sprintf("file:memdbconc%d?mode=memory&cache=shared&_pragma=busy_timeout(10000)", serviceTestDBCounter+1)
	db, err := gorm.Open(sqlite.Open(concDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Plot{}, &model.PlantingPlan{}, &model.HarvestRecord{},
		&model.DiaryEntry{}, &model.DiaryComment{}, &model.CommunityPost{}, &model.CommunityComment{},
		&model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	plotSvc, _ := newPlotService(t, db)
	owner := newTestUser(t, db, "conc-owner", "farmer")
	_ = newTestUser(t, db, "conc-admin", "admin")
	plot := newTestPlot(t, db, "P-CONC", "adopted", &owner.ID)
	plan := seedCompletedPlan(t, db, plot.ID, owner.ID)
	seedHarvest(t, db, plan.ID, owner.ID)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return plotSvc.RecomputeReleaseStatus(tx, plot.ID)
	}); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var ok1, ok2 bool
	var err1, err2 error
	// 认养人与管理员并发释放同一地块，二者都有释放权限
	release := func(operator uint, role string, ok *bool, perr *error) {
		defer wg.Done()
		<-start
		_, err := plotSvc.Release(plot.ID, operator, role)
		*ok = err == nil
		*perr = err
	}
	adminID := uint(999999)
	wg.Add(2)
	go release(owner.ID, "farmer", &ok1, &err1)
	go release(adminID, "admin", &ok2, &err2)
	close(start)
	wg.Wait()

	// 认养人成功、另一人必然失败（403/409/锁竞争）；反之亦然。
	successes := 0
	if ok1 {
		successes++
	}
	if ok2 {
		successes++
	}
	if successes > 1 {
		t.Fatalf("concurrent release: both succeeded err1=%v err2=%v", err1, err2)
	}
	if successes == 0 {
		t.Fatalf("concurrent release: neither succeeded err1=%v err2=%v", err1, err2)
	}
	final := &model.Plot{}
	if err := db.First(final, plot.ID).Error; err != nil {
		t.Fatalf("reload plot: %v", err)
	}
	if final.Status != "available" || final.AdopterID != nil {
		t.Fatalf("final plot state invalid: status=%s adopter=%v", final.Status, final.AdopterID)
	}
}

func TestPlotService_RecomputeTransitions(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	owner := newTestUser(t, db, "farmer-r", "farmer")
	uid := owner.ID
	plot := newTestPlot(t, db, "P-RECOMP", "adopted", &uid)

	recompute := func() {
		t.Helper()
		if err := db.Transaction(func(tx *gorm.DB) error {
			return svc.RecomputeReleaseStatus(tx, plot.ID)
		}); err != nil {
			t.Fatalf("recompute: %v", err)
		}
	}

	// 无计划无收成 -> adopted
	recompute()
	assertPlotUnchanged(t, db, plot.ID, "adopted", uid)

	// 计划未完成 + 有收成 -> 仍 adopted
	plan := &model.PlantingPlan{PlotID: plot.ID, UserID: uid, CropName: "番茄", CropType: "vegetable", Season: "summer", Status: "harvesting"}
	if err := db.Create(plan).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	seedHarvest(t, db, plan.ID, uid)
	recompute()
	assertPlotUnchanged(t, db, plot.ID, "adopted", uid)

	// 计划完成但收成还在（对应同一计划）-> harvested
	plan.Status = "completed"
	if err := db.Save(plan).Error; err != nil {
		t.Fatalf("complete plan: %v", err)
	}
	recompute()
	assertPlotUnchanged(t, db, plot.ID, "harvested", uid)

	// 删除最后一条收成 -> 回到 adopted（认养关系保留）
	if err := db.Where("plan_id = ?", plan.ID).Delete(&model.HarvestRecord{}).Error; err != nil {
		t.Fatalf("delete harvest: %v", err)
	}
	recompute()
	assertPlotUnchanged(t, db, plot.ID, "adopted", uid)
}

func assertPlotUnchanged(t *testing.T, db *gorm.DB, plotID uint, wantStatus string, wantAdopter uint) {
	t.Helper()
	p := &model.Plot{}
	if err := db.First(p, plotID).Error; err != nil {
		t.Fatalf("reload plot %d: %v", plotID, err)
	}
	if p.Status != wantStatus {
		t.Fatalf("plot %d status=%s, want %s", plotID, p.Status, wantStatus)
	}
	if p.AdopterID == nil || *p.AdopterID != wantAdopter {
		t.Fatalf("plot %d adopter=%v, want %d (adoption relation must be kept)", plotID, p.AdopterID, wantAdopter)
	}
}

func TestPlotService_AdoptUsesPageQuery(t *testing.T) {
	// 验证 List 分页复用
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	for i := 0; i < 3; i++ {
		newTestPlot(t, db, fmt.Sprintf("P-LIST-%d", i), "available", nil)
	}
	plots, total, err := svc.List(util.PageQuery{Page: 1, PageSize: 2}, "")
	if err != nil || total != 3 {
		t.Errorf("List total=%d err=%v", total, err)
	}
	if len(plots) != 2 {
		t.Errorf("List len=%d, want 2", len(plots))
	}
}
