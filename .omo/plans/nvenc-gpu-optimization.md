# nvenc-gpu-optimization - Work Plan

## TL;DR (For humans)

**What you'll get:** 一份完整的 GPU 硬编码优化分析文档（`.omo/output/nvenc-gpu-optimization.md`），涵盖从当前 CPU 管线瓶颈分析到 ddagrab + NVENC 零拷贝方案的全链路优化建议。

**Why this approach:** 基于三份独立研究交叉验证（ffmpeg NVENC 优化、GPU 零拷贝管线、Pion WebRTC 优化），所有建议均来自 NVIDIA 官方文档和实测数据。核心收益：CPU 占用从 30-50% 降至 ~5-10%。

**What it will NOT do:** 不修改任何产品代码，不推荐已弃用技术（NvFBC、CUDA IPC on WDDM、GPUDirect on GeForce）。

**Effort:** Quick
**Risk:** Low - 纯文档交付物
**Decisions to sanity-check:** 60fps 捕获、h264_nvenc 编码器、ddagrab 捕获源、仅分析文档不修改代码

文档已完成，路径 `.omo/output/nvenc-gpu-optimization.md`。完整细节如下。

---

> TL;DR (machine): Quick effort, low risk, produce GPU NVENC optimization analysis document — corrected with lavfi path, baseline profile, removed -pix_fmt d3d11/-zerolatency/-flush_packets

## Scope
### Must have
- 一份完整的 GPU 硬编码优化分析 Markdown 文档，覆盖：
  - 现状管线瓶颈分析
  - ddagrab + NVENC 替代 gdigrab + libx264 的架构方案
  - 完整的 ffmpeg 优化命令及逐参数说明
  - Pion WebRTC 适配建议（ICE-Lite、MTU、H.264 packetization）
  - Go 侧缓冲/NAL 解析优化建议（sync.Pool、滑动窗口、环形缓冲）
  - 性能对比预估（优化前后 CPU/GPU/延迟）
  - 故障排查指南

### Must NOT have (guardrails, anti-slop, scope boundaries)
- 不修改任何产品代码
- 不含可执行的代码实现
- 不涉及操作系统或驱动层面的修改建议
- 不推荐已弃用的技术路径（NvFBC、CUDA IPC on WDDM、GPUDirect on GeForce）

## Verification strategy
> Zero human intervention - all verification is agent-executed.
- Test decision: none (文档验证通过人工阅读)
- Evidence: 文档文件路径 `.omo/output/nvenc-gpu-optimization.md`

## Execution strategy
### Parallel execution waves
> 单项任务，无需分波。

### Dependency matrix
| Todo | Depends on | Blocks | Can parallelize with |
| --- | --- | --- | --- |
| T1 | 研究数据 (已完成) | 无 | 无 |

## Todos
> Implementation + Test = ONE todo. Never separate.
- [x] 1. 撰写 GPU 硬编码优化分析文档
  What to do: 将三份并行研究的结果与代码库分析合成，撰写完整的优化分析文档，输出到 `.omo/output/nvenc-gpu-optimization.md`
  Must NOT do: 不修改任何产品代码（ffmpeg/capture.go, webrtc/manager.go 等）
  Parallelization: Wave 1 | Blocked by: 无 | Blocks: 无
  References:
    - 研究结果 bg_4655b07a (ffmpeg NVENC 优化)
    - 研究结果 bg_ac638a06 (GPU 零拷贝管线)
    - 研究结果 bg_77464d11 (Pion WebRTC 优化)
    - 当前代码库: ffmpeg/capture.go, webrtc/manager.go, config/config.go, config.yaml
  Acceptance criteria: 文档包含以下 7 个章节且每个章节有实质性内容：
    1. 现状分析与瓶颈定位
    2. GPU 硬件编码管线架构
    3. ffmpeg 优化命令与参数详解
    4. Pion WebRTC 适配建议
    5. Go 侧缓冲与解析优化建议
    6. 性能对比预估
    7. 故障排查指南
  QA: 文档存在性检查 - 文件 `.omo/output/nvenc-gpu-optimization.md` 非空且大小 >5KB
  Commit: Y | docs: add GPU NVENC optimization analysis document

## Final verification wave
> Runs in parallel after ALL todos. ALL must APPROVE. Surface results and wait for the user's explicit okay before declaring complete.
- [x] F1. Plan compliance audit — 文档包含全部 7 个必需章节，所有建议有研究来源
- [x] F2. Code quality review — 本文档不涉及代码，无需代码审查
- [x] F3. Real manual QA — 文档已生成，路径 `.omo/output/nvenc-gpu-optimization.md`
- [x] F4. Scope fidelity — 未修改任何产品代码，未推荐弃用技术

## Commit strategy
单次提交，提交信息: `docs: add GPU NVENC optimization analysis for RTX 3080`

## Success criteria
- 文档文件存在且非空
- 文档包含全部 7 个必需章节
- 文档中的技术建议均有研究来源支撑
- 不包含已弃用的技术路径推荐（NvFBC、CUDA IPC on WDDM 等）
