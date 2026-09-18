package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/model"
	"github.com/communitygarden/server/internal/repository"
	"github.com/communitygarden/server/internal/util"
)

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

// seedAdoptedPlot 准备一个已认养地块。
func seedAdoptedPlot(t *testing.T, db *gorm.DB, code string, svc *PlotService) (*model.Plot, *model.User) {
	t.Helper()
	user := newTestUser(t, db, "owner-"+code, "farmer")
	plot := newTestPlot(t, db, code, "available", nil)
	if _, err := svc.Adopt(plot.ID, user.ID, "farmer", user.Username); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return plot, user
}

func createPlanForPlot(t *testing.T, db *gorm.DB, plotID, userID uint, status string) *model.PlantingPlan {
	t.Helper()
	now := time.Now()
	p := &model.PlantingPlan{
		PlotID: plotID, UserID: userID, CropName: "番茄", CropType: "vegetable",
		Season: "summer", Status: status, PlantDate: &now, ExpectedHarvestDate: &now,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return p
}

func createHarvestForPlan(t *testing.T, db *gorm.DB, plan *model.PlantingPlan, userID uint) *model.HarvestRecord {
	t.Helper()
	h := &model.HarvestRecord{
		PlanID: plan.ID, UserID: userID, CropName: plan.CropName,
		HarvestDate: time.Now(), WeightKg: 2.5, Quality: "good",
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("create harvest: %v", err)
	}
	return h
}

// TestPlotService_ReleasePreconditions 覆盖释放前置条件的全部组合。
func TestPlotService_ReleasePreconditions(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)

	// 场景 1：已认养，但没有种植计划 -> 不可释放，保持 adopted + 认养关系
	pNoPlan, ownerNoPlan := seedAdoptedPlot(t, db, "P-NO-PLAN", svc)

	// 场景 2：有计划但计划未完成（growing）-> 不可释放
	pOngoing, ownerOngoing := seedAdoptedPlot(t, db, "P-ONGOING", svc)
	createPlanForPlot(t, db, pOngoing.ID, ownerOngoing.ID, "growing")

	// 场景 3：计划已完成但没有任何收成记录 -> 不可释放，保持 adopted
	pDoneNoHarvest, ownerDone := seedAdoptedPlot(t, db, "P-DONE-NOHV", svc)
	createPlanForPlot(t, db, pDoneNoHarvest.ID, ownerDone.ID, "completed")

	// 场景 4：有已完成的计划，但收成记录只挂在另一条未完成的计划上 -> 不可释放，保持 adopted
	pHvOngoing, ownerHv := seedAdoptedPlot(t, db, "P-HV-ONGOING", svc)
	createPlanForPlot(t, db, pHvOngoing.ID, ownerHv.ID, "completed")
	ongoingPlan := createPlanForPlot(t, db, pHvOngoing.ID, ownerHv.ID, "growing")
	createHarvestForPlan(t, db, ongoingPlan, ownerHv.ID)

	// 场景 5：已完成计划 + 至少一条关联收成 -> 可释放
	pReady, ownerReady := seedAdoptedPlot(t, db, "P-READY", svc)
	completedPlan := createPlanForPlot(t, db, pReady.ID, ownerReady.ID, "completed")
	createHarvestForPlan(t, db, completedPlan, ownerReady.ID)
	// 标记为 harvested（与 RefreshHarvestReadyTx 在真实流程中的结果一致）
	if err := svc.RefreshHarvestReadyTx(db, pReady.ID, "test"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	blockedCases := []struct {
		name       string
		plotID     uint
		operatorID uint
		wantReason constants.PlotReleaseBlocker
	}{
		{name: "no plan", plotID: pNoPlan.ID, operatorID: ownerNoPlan.ID, wantReason: constants.ReleaseBlockerNoPlan},
		{name: "plan ongoing", plotID: pOngoing.ID, operatorID: ownerOngoing.ID, wantReason: constants.ReleaseBlockerPlanOngoing},
		{name: "completed plan no harvest", plotID: pDoneNoHarvest.ID, operatorID: ownerDone.ID, wantReason: constants.ReleaseBlockerNoHarvest},
		{name: "harvest on unfinished plan", plotID: pHvOngoing.ID, operatorID: ownerHv.ID, wantReason: constants.ReleaseBlockerHarvestOnUnfinishedPlan},
	}
	for _, bc := range blockedCases {
		t.Run("blocked/"+bc.name, func(t *testing.T) {
			// 释放入口（eligibility）必须明确为不可释放且给出缺失项。
			e, err := svc.ReleaseEligibility(bc.plotID)
			if err != nil {
				t.Fatalf("eligibility: %v", err)
			}
			if e.Releasable {
				t.Fatalf("expected not releasable for %s", bc.name)
			}
			if constants.PlotReleaseBlocker(e.ReasonCode) != bc.wantReason {
				t.Errorf("reason_code=%s want=%s", e.ReasonCode, bc.wantReason)
			}
			if e.Reason == "" {
				t.Errorf("expected human-readable reason for %s", bc.name)
			}

			// 直接调用释放必须失败。
			if _, err := svc.Release(bc.plotID, bc.operatorID, "farmer"); err == nil {
				t.Fatalf("expected release error for %s", bc.name)
			} else {
				var ae *util.AppError
				if !errors.As(err, &ae) || ae.Code != constants.CodePlotReleasePrecondition {
					t.Fatalf("expected CodePlotReleasePrecondition, got %v", err)
				}
			}

			// 失败后：地块必须保持已认养，认养关系不得被清除。
			p, err := svc.GetByID(bc.plotID)
			if err != nil {
				t.Fatalf("get plot: %v", err)
			}
			if p.Status == string(constants.PlotStatusAvailable) {
				t.Errorf("%s: plot must not become available after failed release", bc.name)
			}
			if p.AdopterID == nil || *p.AdopterID != bc.operatorID {
				t.Errorf("%s: adoption relation must remain intact, adopter=%v", bc.name, p.AdopterID)
			}
		})
	}

	t.Run("success/completed plan with harvest", func(t *testing.T) {
		got, err := svc.Release(pReady.ID, ownerReady.ID, "farmer")
		if err != nil {
			t.Fatalf("Release: %v", err)
		}
		if got.Status != string(constants.PlotStatusAvailable) || got.AdopterID != nil {
			t.Errorf("release result invalid: status=%s adopter=%v", got.Status, got.AdopterID)
		}
	})

	t.Run("non-owner forbidden and state unchanged", func(t *testing.T) {
		other := newTestUser(t, db, "intruder", "citizen")
		_, err := svc.Release(pOngoing.ID, other.ID, "citizen")
		if err == nil {
			t.Fatalf("expected forbidden")
		}
		p, _ := svc.GetByID(pOngoing.ID)
		if p.Status != string(constants.PlotStatusAdopted) || p.AdopterID == nil || *p.AdopterID != ownerOngoing.ID {
			t.Errorf("non-owner attempt mutated plot: status=%s adopter=%v", p.Status, p.AdopterID)
		}
	})
}

// TestPlotService_ReleaseConcurrent 验证两名操作者并发释放同一地块，只能一人成功，
// 失败方不会清除认养关系或改变状态。
//
// 说明：生产环境使用 PostgreSQL，SELECT ... FOR UPDATE 提供行级锁，两个事务可以真正
// 并发持锁竞争；单元测试使用内存 SQLite，其 FOR UPDATE 会退化为整表写锁，多连接并发会
// 触发 "database is deadlocked"。因此这里把连接池限制为 1：调用方仍以 goroutine 并发发起，
// SQLite 在底层串行化事务——先拿到连接的事务完成释放，后到的事务读取到 available 后走
// “已被并发释放”冲突分支。该测试覆盖的是 service 层的互斥判定与失败者不产生副作用；
// PostgreSQL 的行锁互斥由 FOR UPDATE 在数据库层保证。
func TestPlotService_ReleaseConcurrent(t *testing.T) {
	db := newTestServiceDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get underlying db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1) // 串行化 SQLite 事务，模拟持锁竞争的先后顺序
	defer sqlDB.SetMaxOpenConns(0)

	svc, _ := newPlotService(t, db)
	plot, owner := seedAdoptedPlot(t, db, "P-CONCURRENT", svc)
	plan := createPlanForPlot(t, db, plot.ID, owner.ID, "completed")
	createHarvestForPlan(t, db, plan, owner.ID)
	if err := svc.RefreshHarvestReadyTx(db, plot.ID, "test"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	const n = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start // 同时起跑，尽量制造竞争
			_, errs[idx] = svc.Release(plot.ID, owner.ID, "farmer")
		}(i)
	}
	close(start)
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		default:
			var ae *util.AppError
			if errors.As(err, &ae) && (ae.Code == constants.CodePlotStateConflict || ae.Code == constants.CodePlotReleasePrecondition) {
				conflicts++
			}
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 success, got %d (conflicts=%d, errs=%v)", successes, conflicts, errs)
	}
	if conflicts != 1 {
		t.Fatalf("expected exactly 1 losing conflict, got %d (errs=%v)", conflicts, errs)
	}

	// 最终状态必须是 available 且认养关系已清空（仅由成功者执行一次）。
	p, err := svc.GetByID(plot.ID)
	if err != nil {
		t.Fatalf("get plot: %v", err)
	}
	if p.Status != string(constants.PlotStatusAvailable) {
		t.Errorf("final status=%s want available", p.Status)
	}
	if p.AdopterID != nil {
		t.Errorf("adopter must be cleared after single successful release, got %v", *p.AdopterID)
	}

	// 再释放一次必须继续冲突，且地块保持 available（不会重复清理/写脏）。
	if _, err := svc.Release(plot.ID, owner.ID, "farmer"); err == nil {
		t.Errorf("re-release must conflict")
	}
}

// TestPlotService_ReleaseLoserKeepsAdoption 确定性验证：成功者释放后，失败者再尝试时
// 既不能清除认养关系（已无认养关系可清），也不能把状态从 available 改回其它值。
func TestPlotService_ReleaseLoserKeepsAdoption(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	plot, owner := seedAdoptedPlot(t, db, "P-LOSER", svc)
	plan := createPlanForPlot(t, db, plot.ID, owner.ID, "completed")
	createHarvestForPlan(t, db, plan, owner.ID)
	if err := svc.RefreshHarvestReadyTx(db, plot.ID, "test"); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if _, err := svc.Release(plot.ID, owner.ID, "farmer"); err != nil {
		t.Fatalf("first release should succeed: %v", err)
	}
	// 第二操作者（这里模拟为晚到的并发请求）必须失败。
	if _, err := svc.Release(plot.ID, owner.ID, "farmer"); err == nil {
		t.Fatalf("second release must fail with state conflict")
	}
	p, _ := svc.GetByID(plot.ID)
	if p.Status != string(constants.PlotStatusAvailable) || p.AdopterID != nil {
		t.Errorf("losing release must not mutate plot: status=%s adopter=%v", p.Status, p.AdopterID)
	}
}

// TestPlotService_StateTransitions 验证地块状态随计划/收成变化的关键转换。
func TestPlotService_StateTransitions(t *testing.T) {
	db := newTestServiceDB(t)
	svc, _ := newPlotService(t, db)
	plot, owner := seedAdoptedPlot(t, db, "P-TRANS", svc)

	// 完成计划但无收成 -> 仍 adopted
	plan := createPlanForPlot(t, db, plot.ID, owner.ID, "completed")
	if err := svc.RefreshHarvestReadyTx(db, plot.ID, "plan_completed"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	p, _ := svc.GetByID(plot.ID)
	if p.Status != string(constants.PlotStatusAdopted) {
		t.Fatalf("completed plan without harvest: status=%s want adopted", p.Status)
	}

	// 录入关联到已完成计划的收成 -> harvested（可释放）
	createHarvestForPlan(t, db, plan, owner.ID)
	if err := svc.RefreshHarvestReadyTx(db, plot.ID, "harvest_created"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	p, _ = svc.GetByID(plot.ID)
	if p.Status != string(constants.PlotStatusHarvested) {
		t.Fatalf("completed plan + harvest: status=%s want harvested", p.Status)
	}
	e, _ := svc.ReleaseEligibility(plot.ID)
	if !e.Releasable {
		t.Fatalf("expected releasable after completed plan + harvest")
	}

	// 删除收成后撤回释放入口 -> 回到 adopted
	harvestRepo := repository.NewHarvestRecordRepository(db)
	harvestSvc := NewHarvestRecordService(harvestRepo, repository.NewPlantingPlanRepository(db), svc, db, testLogger())
	var hs []model.HarvestRecord
	if err := db.Where("plan_id = ?", plan.ID).Find(&hs).Error; err != nil {
		t.Fatalf("find harvests: %v", err)
	}
	if err := harvestSvc.Delete(hs[0].ID, owner.ID, "farmer"); err != nil {
		t.Fatalf("delete harvest: %v", err)
	}
	p, _ = svc.GetByID(plot.ID)
	if p.Status != string(constants.PlotStatusAdopted) || p.AdopterID == nil {
		t.Fatalf("after deleting last harvest: status=%s adopter=%v want adopted+adopter", p.Status, p.AdopterID)
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
