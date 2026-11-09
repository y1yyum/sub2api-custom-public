// Package quality 对保存的监测结果执行有界、确定性的离线评分。
//
// 字形模板由操作者在 baseline JSON 中显式提供；来源格式固定为
// operator-authored-grid-v1，不隐式加载系统字体，也不调用 OCR、模型或网络。
// 每题最多 8 个模板，目标字按 QuestionRef 绑定；未知题保持中性。网格采用
// 全角 ASCII 折叠为一格、制表位 4 格和一字符一格的布局约定，比较前保持
// 等比居中，允许正负一像素平移，不允许镜像、旋转或非等比拉伸。
//
// calibration.options.dataset_origin=human、label_source=human 和 labeler
// 是操作者对人工标注来源的声明；纯离线程序无法验证标注者身份或真实性。
// 报告始终写 human_provenance_verified=false、production_data=Unknown。
// synthetic 可以用于回归和算法自测，但永不授予 CalibratedCoverage。
// 固定判定阈值为预测 >=80、人工正确 >=80、人工错误 <=40；留出检验不调参。
//
// 模型、题目文本/ID、目标字、任务和重复响应均防跨 split 泄漏。同一题的
// 重复规范化响应在任一 split 内也不能增加样本数。泛化覆盖仅授予真正出现在
// holdout 且达到最低人工正负样本、排序和误报门槛的 model×question×style。
// style 同时绑定规范化绘制字符集合的 SHA256，不把 x 风格覆盖扩大到点阵等
// 未标注字符风格；风格摘要不包含可读正文。
// Reference 的 median/MAD 描述该留出单元的人工正确样本，不回灌训练或调阈值。
// 只有覆核过的 style 才出高置信；调用方还须独立执行跨任务/多题趋势规则。
//
// BaselineSHA256 是输入 JSON 全部原始字节的 SHA256（包括空白）；DataDigest
// 则绑定该基线 digest、固定算法版本、参数及排序后的标签记录。报告本身不是
// baseline 的输入字段，不存在报告 digest 自包含。修改标签、模板或字节格式
// 都会改变 BaselineSHA256；调用方负责 config_revision fencing。
//
// 评分结果仅含原结果引用、response SHA256、分数和诊断，不保留原文。
// 超长响应在正文哈希前拒绝，SourceSHA256 留空；有界题目仍保留 QuestionRef。
// 题目元数据本身超限时不计算题目哈希。低分不代表健康故障；本包没有
// 健康状态、权重、存储、网络或调用模型的接口。
package quality
