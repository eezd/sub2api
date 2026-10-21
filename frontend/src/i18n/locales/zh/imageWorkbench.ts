export default {
  imageWorkbench: {
    loading: '加载中…',
    title: '生图工具台', intro: '选择自己的密钥与模型，用文字和参考图创作图片。',
    refreshModels: '刷新模型列表', unusedDrafts: '尚未使用的上传图片（仍占空间）', add: '添加', deleteDraft: '删除',
    key: 'API 密钥', selectKey: '请选择自己的密钥', protocol: '接口协议', model: '模型', selectModel: '请选择模型',
    modelHint: '模型列表不代表每个模型都支持生图，具体能力和参数以所选分组为准。',
    prompt: '提示词', promptPlaceholder: '描述希望生成的内容，最多 8000 字', size: '尺寸', quality: '质量', ratio: '画面比例', resolution: '分辨率',
    auto: '自动', low: '低', medium: '中', high: '高', count: '生成数量',
    references: '参考图', upload: '上传图片', referenceHint: '支持上传、拖拽或在此粘贴 PNG / JPEG / WebP；每张最多 20 MiB，最多 5 张。',
    removeReference: '移除参考图', referenceCount: '参考图最多 5 张，请先移除部分图片。', fileError: '请选择非空的 PNG、JPEG 或 WebP 图片，单张不超过 20 MiB。',
    storage: '生图空间 {used} / 100 MB，正在预留 {reserved} MB（与 Chat 分开）',
    reserveHint: '本次最多预留 {size} MB，完成后按实际大小计算；空间不足时请先清理历史或参考图。',
    generate: '开始生成', processing: '处理中…', running: '后台生成中', waiting: '等待生成结果…',
    billingNotice: '按所选密钥分组正常计费。离开页面后任务继续；失败不自动重试，取消也可能已产生费用。',
    results: '生成结果', empty: '输入提示词并选择模型，即可开始创作。', resultAlt: '生成图片 {n}', download: '下载', useReference: '作为参考图', reuse: '复用参数',
    cancel: '取消任务', cancelling: '正在取消，请等待任务结束；上游可能已经计费。',
    history: '生成历史', noHistory: '暂无生成历史', more: '加载更多', delete: '删除记录和独占图片', retryDelete: '重试删除',
    deleteConfirm: '删除此任务及仅被它使用的图片？已被其他任务引用的图片会保留。', close: '关闭', preview: '图片预览',
    checkSubmission: '核对本次提交（不会重复生成）', networkError: '请求失败，请检查网络后重试。',
    status: { running: '生成中', pending: '等待中', complete: '已完成', partial: '部分成功', failed: '失败', cancelled: '已取消', interrupted: '已中断' }
  }
}
