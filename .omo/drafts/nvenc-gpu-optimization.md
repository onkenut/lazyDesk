# Draft: nvenc-gpu-optimization

## Intent
- **intent**: CLEAR
- **review_required**: false
- **request**: 使用 ffmpeg + NVIDIA RTX 3080 硬编码 + pipe + WebRTC 实现局域网远程桌面控制的 GPU 优化方案文档

## Decisions
- **捕获帧率**: 60fps
- **交付物**: 仅优化分析文档（Markdown），不含代码修改计划
- **Go 侧优化**: 写入文档内的优化建议，不实施代码改动
- **编码器**: h264_nvenc（WebRTC 浏览器兼容要求 H.264）
- **捕获方式**: ddagrab（DXGI Desktop Duplication）
- **NVENC 预设**: p1 + tune ll + CBR 码率控制
- **像素管线**: D3D11 GPU 纹理零拷贝路径
- **IPC**: stdout pipe（保持现有方式）

## Status
- **status**: complete — document written to .omo/output/nvenc-gpu-optimization.md (729 lines)

## Research Sources
- bg_4655b07a: ffmpeg NVENC optimization (complete)
- bg_ac638a06: GPU zero-copy pipeline (complete)
- bg_77464d11: Pion WebRTC optimization (complete)
