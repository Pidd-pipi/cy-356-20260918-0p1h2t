// 与后端 internal/constants/enums.go 对应的共享枚举（新增枚举值需前后端同步修改 ≥10 处）

export type RoleType = 'admin' | 'farmer' | 'citizen'
export const RoleText: Record<string, string> = {
  admin: '管理员',
  farmer: '农场主',
  citizen: '城市居民'
}

export type PlotStatus = 'available' | 'adopted' | 'harvested'
export const PlotStatusMeta: Record<string, { label: string; type: 'success' | 'warning' | 'info' | 'danger' | 'primary' }> = {
  available: { label: '空闲可认养', type: 'success' },
  adopted: { label: '已认养', type: 'warning' },
  harvested: { label: '可释放（已收成）', type: 'info' }
}

// 地块释放入口不可用原因（与后端 PlotReleaseBlocker 枚举对应，驱动按钮禁用与提示）。
export type PlotReleaseBlocker =
  | 'not_adopted'
  | 'no_plan'
  | 'plan_ongoing'
  | 'no_harvest'
  | 'harvest_unfinished_plan'
export const PlotReleaseReasonText: Record<string, string> = {
  not_adopted: '该地块当前未处于认养中，无需释放',
  no_plan: '还没有种植计划，需完成一次种植并录入收成后方可释放',
  plan_ongoing: '种植计划尚未完成，完成种植计划后才能释放',
  no_harvest: '已完成的种植计划还没有收成记录，至少记录一条收成后方可释放',
  harvest_unfinished_plan: '收成记录对应的种植计划尚未完成，需有收成记录关联到已完成的计划后才能释放'
}

export type PlanStatus = 'planned' | 'planting' | 'growing' | 'harvesting' | 'completed'
export const PlanStatusMeta: Record<string, { label: string; type: 'success' | 'warning' | 'info' | 'danger' | 'primary' }> = {
  planned: { label: '已计划', type: 'info' },
  planting: { label: '播种中', type: 'primary' },
  growing: { label: '生长中', type: 'warning' },
  harvesting: { label: '采收中', type: 'danger' },
  completed: { label: '已完成', type: 'success' }
}
// 状态机（与后端 PlanStatusTransitions 对应，驱动按钮显隐）
export const PlanStatusNext: Record<string, string> = {
  planned: 'planting',
  planting: 'growing',
  growing: 'harvesting',
  harvesting: 'completed',
  completed: ''
}
export const PlanStatusActions: Record<string, string> = {
  planned: '开始播种',
  planting: '进入生长期',
  growing: '开始采收',
  harvesting: '标记完成',
  completed: ''
}

export type CropType = 'vegetable' | 'fruit' | 'herb'
export const CropTypeText: Record<string, string> = {
  vegetable: '蔬菜',
  fruit: '水果',
  herb: '香草'
}

export type Season = 'spring' | 'summer' | 'autumn' | 'winter'
export const SeasonText: Record<string, string> = {
  spring: '春季',
  summer: '夏季',
  autumn: '秋季',
  winter: '冬季'
}

export type DiaryAction = 'sowing' | 'watering' | 'fertilizing' | 'pest_control' | 'harvest' | 'other'
export const DiaryActionText: Record<string, string> = {
  sowing: '播种',
  watering: '浇水',
  fertilizing: '施肥',
  pest_control: '除虫',
  harvest: '收成',
  other: '其他'
}

export type PostType = 'experience' | 'pest' | 'recipe' | 'activity'
export const PostTypeText: Record<string, string> = {
  experience: '种植经验',
  pest: '病虫害防治',
  recipe: '食谱创意',
  activity: '线下农耕活动'
}

export type HarvestQuality = 'excellent' | 'good' | 'fair'
export const HarvestQualityText: Record<string, string> = {
  excellent: '优',
  good: '良',
  fair: '一般'
}

export const SoilTypeText: Record<string, string> = {
  loam: '壤土',
  clay: '黏土',
  sand: '沙土',
  black: '黑土'
}

export const SunlightText: Record<string, string> = {
  full: '全日照',
  partial: '半日照',
  shade: '遮阴'
}
