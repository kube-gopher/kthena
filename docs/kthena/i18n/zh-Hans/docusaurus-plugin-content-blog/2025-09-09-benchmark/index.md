---
slug: scoreplugin-benchmark-blog-post
title: "Kthena 路由器 ScorePlugin 架构与基准分析"
authors: [bytebingo]
tags: []
---

# Kthena 路由器 ScorePlugin 架构与基准分析

## 摘要 {#abstract}

本文分析了 **Kthena 路由器** 中 **ScorePlugin** 模块的系统设计与实现，该模块利用可配置、可插拔的架构，实现对推理请求的多维评分和智能路由。我们对目前实现的六个 **ScorePlugins** 进行了详细的分析，并基于 **DeepSeek-R1-Distill-Qwen-7B** 模型构建了标准化基准测试环境，以评估不同调度策略在长短系统提示场景下的性能表现。

实验结果表明，在**长系统提示场景**下，**KVCacheAware 插件 + Least Request 插件**组合实现了**2.73 倍更高的吞吐量**，并将**TTFT 延迟降低了 73.5%**，显著优化了整体推理服务性能，并验证了面向缓存调度在大规模模型推理中的核心价值。

<!-- truncate -->

## 1. 引言 {#1-introduction}

在**大型语言模型推理服务**中，智能请求调度策略对整体系统性能具有决定性影响。Kthena Router 采用**基于评分的调度框架**，通过**可扩展的插件系统**实现多策略负载均衡。本文系统分析了该调度系统的架构设计、核心算法及性能特性。

## 2. 系统架构 {#2-system-architecture}

### 2.1 核心接口设计 {#21-core-interface-design}

Kthena Router 中的调度系统围绕一个统一的 **ScorePlugin** 接口构建：

```go
type ScorePlugin interface {
    Name() string
    Score(ctx *Context, pods []*datastore.PodInfo) map[*datastore.PodInfo]int
}
```

每个插件会为候选 Pod 生成 **归一化分数，范围在 [0, 100] 之间**，分数越高表示该 Pod 对于即将到来的推理请求越适合。

### 2.2 调度执行流程 {#22-scheduling-execution-pipeline}

调度器使用一个 **多阶段流水线架构**，包括以下步骤：

1. **过滤阶段** - 通过 Filter 插件执行资源限制检查和可用性验证
2. **评分阶段** - 多个 Score 插件并行计算 Pod 的适用性分数
3. **加权汇总** - 根据可配置权重合并多插件结果
4. **选择决策** - 根据最终汇总得分选择前 N 个 Pod

## 3. ScorePlugin 实现 {#3-scoreplugin-implementations}

### 3.1 GPU 缓存使用插件 {#31-gpu-cache-usage-plugin}

一种基于 **GPU 缓存利用率** 的资源感知调度策略。

**算法原理：**

- 实时监控每个 Pod 的 **GPU 缓存使用率** (`GPUCacheUsage`)
- 评分函数：
  ```
  Score = (1.0 - GPUCacheUsage) × 100
  ```
- 优先选择 **GPU 缓存利用率较低** 的 Pod，降低内存溢出的风险

**使用场景：** GPU 内存资源有限的环境。

### 3.2 最少请求插件 {#32-least-request-plugin}

一种基于 **活动请求队列长度** 的负载均衡策略。

**算法原理：**

- **指标：**
  - 运行请求数量：`RequestRunningNum`
  - 等待队列长度: `RequestWaitingNum`
- **基础负载计算:**
  ```
  base = RequestRunningNum + 100 × RequestWaitingNum
  ```
- **归一化得分:**
  ```
  score = ((maxScore - baseScores[info]) / maxScore) × 100
  ```

**关键设计细节:**

- 等待队列权重因子设置为 **100**，对积压的 Pods 强烈惩罚
- 启用 **动态负载感知的平衡**

### 3.3 最低延迟插件 {#33-least-latency-plugin}

基于 **推理延迟指标** 的面向性能的调度策略。

**算法原理：**

- **监控指标:**
  - **TTFT** (*首次生成 Token 时间*)
  - **TPOT** (*每个输出 Token 时间*)
- **归一化:**
  ```
  ScoreTTFT = (MaxTTFT - CurrentTTFT) / (MaxTTFT - MinTTFT) × 100
  ScoreTPOT = (MaxTPOT - CurrentTPOT) / (MaxTPOT - MinTPOT) × 100
  ```
- **最终加权得分:**
  ```
  Score = α × ScoreTTFT + (1 - α) × ScoreTPOT
  ```

**配置:**

```yaml
TTFTTPOTWeightFactor: 0.5  # TTFT weight α
```

### 3.4 KVCacheAware 插件 {#34-kvcacheaware-plugin}

一种先进的 **缓存感知调度策略**，利用 **KV 缓存命中率**。

**技术亮点：**

- 使用特定模型分词器的**令牌级语义匹配**
- 通过 Redis 进行**分布式缓存协调**以实现跨 Pod 状态同步
- **优化的连续块匹配算法**以最大化缓存命中率

**算法工作流程：**

1. **分词阶段**
   ```
   Input → Model-specific Tokenizer → Token Sequence [t₁, t₂, ..., tₙ]
   ```

2. **块分割**
   ```
   Token Sequence → Fixed-size Blocks [B₁, B₂, ..., Bₘ]
   Block size: 128 tokens (configurable)
   ```

3. **哈希生成**
   ```
   For each Bᵢ: Hash(Bᵢ) = SHA256(Bᵢ) → hᵢ
   ```

4. **分布式缓存查询**
   ```
   Redis pipeline query:
   For each hᵢ → {Pod₁: timestamp₁, Pod₂: timestamp₂, ...}
   ```

5. **连续匹配得分**
   ```
   Score = (Number of contiguous matching blocks / Total blocks) × 100
   ```

**配置示例：**

```yaml
blockSizeToHash: 128      # Token block size
maxBlocksToMatch: 128     # Maximum blocks to process
```

**Redis 键结构：**

```
Key Pattern: "matrix:kv:block:{model}@{hash}"
Value Structure:
{
  "pod-name-1.namespace": "1703123456",
  "pod-name-2.namespace": "1703123789"
}
```

### 3.5 前缀缓存插件 {#35-prefix-cache-plugin}

一种**基于前缀的缓存感知调度策略**。

**架构设计：**

- **三级映射**：`Model → Hash → Pod`
- **基于 LRU 的缓存淘汰**用于高效内存管理
- **Top-K 前缀匹配策略** 用于返回具有最长前缀匹配的前几个 Pods

**主要特性：**

- 字节级前缀匹配算法
- 优化的内存中 LRU 实现
- 可配置的缓存容量和候选项大小

### 3.6 随机插件 {#36-random-plugin}

一种 **轻量级随机负载均衡策略**。

**实现：**

- 生成均匀分布的随机分数：
  ```
  Score ~ Uniform(0, 100)
  ```
- 线程安全的独立随机数生成器
- 无状态设计确保长期均衡行为

**使用场景：**

- 没有明确模式的混合工作负载
- 轻量级部署，避免复杂的调度开销
- 建立基线性能比较

## 4. 实验设计与性能评估 {#4-experimental-design-and-performance-evaluation}

### 4.1 实验设置 {#41-experimental-setup}

使用 **DeepSeek-R1-Distill-Qwen-7B** 模型构建了标准化基准环境，以评估不同调度策略的性能。

**表 1：实验环境配置**

| 参数 | 数值 |
| :--------------------- | :-------------------------------------- |
| 模型 | deepseek-ai/DeepSeek-R1-Distill-Qwen-7B |
| 块大小 | 128                                     |
| 最大模型长度 | 32,768                                  |
| 最大批次 token 数 | 65,536                                  |
| 副本数 | 3                                       |
| GPU 内存利用率 | 0.9                                     |
| 最大序列数 | 256                                     |
| 数据集 | generated-shared-prefix |
| 请求组 | 256                                     |
| 每组请求数 | 32                                      |
| 请求速率 | 800 req/s |
| 最大并发数 | 300                                     |

### 4.2 长系统提示场景（4096 令牌） {#42-long-system-prompt-scenario-4096-tokens}

**表 2：性能指标 – 长提示**

| 插件配置 | 运行次数 | 成功率（%） | 吞吐量（请求/秒） | 延迟（秒） | TTFT（秒） |
| :--------------------------- | :---: | :--------------: | :----------------: | :---------: | :------: |
| 最少请求 + KVCacheAware |   3   |      100.0       |     **32.22**      |  **9.22**   | **0.57** |
| 最少请求 + 前缀缓存 |   3   |      100.0       |       23.87        |    12.47    |   0.83   |
| 随机 |   3   |      100.0       |       11.81        |    25.23    |   2.15   |
| 最少请求 |   3   |      100.0       |        9.86        |    30.13    |  12.46   |
| GPU 使用率 |   3   |      100.0       |        9.56        |    30.92    |  13.14   |
| 最少延迟 |   3   |      100.0       |        9.47        |    31.44    |  11.07   |

### 4.3 短系统提示场景（256 令牌） {#43-short-system-prompt-scenario-256-tokens}

**表 3：性能指标 – 短提示**

| 插件配置 | 运行次数 | 成功率（%） | 吞吐量（请求/秒） | 延迟（秒） | TTFT（秒） |
| :--------------------------- | :---: | :--------------: | :----------------: | :---------: | :------: |
| 最少请求 + 前缀缓存 |   3   |      100.0       |        8.30        |    35.84    |  11.00   |
| 随机 |   3   |      100.0       |        8.21        |    36.25    |   4.54   |
| 最少请求 + KVCacheAware |   3   |      100.0       |        8.08        |    36.67    |  12.19   |
| 最少请求 |   3   |      100.0       |        7.98        |    37.27    |  13.88   |
| GPU 使用率 |   3   |      100.0       |        7.77        |    38.15    |  15.03   |
| 最少延迟 |   3   |      100.0       |        7.09        |    41.91    |  15.46   |

### 4.4 改进的可视化 {#44-visualization-of-improvements}

**表 4：性能提升 vs. 基线 (随机插件)**

| 插件配置 | 吞吐量提升 | TTFT 改进 | 延迟降低 |
| :--------------------------- | :-------------: | :--------------: | :---------------: |
| 最少请求 + KVCacheAware |    **+173%**    |    **-73.5%**    |    **-63.5%**     |
| 最少请求 + 前缀缓存 |      +102%      |      -61.4%      |      -50.6%       |

**KVCacheAware 组合策略**在长前缀场景下显示出**显著的优势**，尤其是在吞吐量和 TTFT 指标上。

## 5. 结果与讨论 {#5-results-and-discussion}

### 5.1 缓存感知策略的优势 {#51-advantages-of-cache-aware-strategies}

结果清楚地展示了缓存感知调度在长提示下的性能优势：

- 使用 **KVCacheAware + 最少请求** 的**峰值吞吐量**达到 32.22 请求/秒，比随机基线提高了**173%**
- **TTFT 减少** 从 2.15 秒降至 0.57 秒（**下降 73.5%**）
- **端到端延迟** 从 25.23 秒降至 9.22 秒（**提升 63.5%**）

### 5.2 场景适应性 {#52-scenario-adaptability}

**长前缀场景（4096 令牌）：**

- 关注缓存的策略显著优于传统平衡方法
- 令牌级块匹配最大化缓存利用
- 分布式缓存协调防止 Pod 之间的重复计算

**短前缀场景（256 令牌）：**

- 各策略之间的性能差异很小
- 有限的前缀长度限制了缓存命中机会
- 传统策略提供稳定的结果

### 5.3 设计权衡 {#53-design-trade-offs}

**计算开销与性能提升：**

- KVCacheAware 引入了来自 **分词** 和 **Redis 查询** 的额外开销
- 在高缓存命中率的环境中，性能提升远远超过额外的成本
- 建议根据工作负载特征采用 **自适应策略选择**

## 6. 部署建议 {#6-deployment-recommendations}

### 6.1 高缓存命中场景 {#61-high-cache-hit-scenarios}

**推荐配置：** `KVCacheAware Plugin + Least Request Plugin`

**使用场景：**

- 客服聊天机器人、代码生成及类似的结构化工作负载
- 带有长系统提示的多轮对话
- 基于模板的内容生成服务

### 6.2 一般负载均衡场景 {#62-general-load-balancing-scenarios}

**推荐配置：** `Least Request Plugin + Least Latency Plugin`

**使用场景：**

- 请求模式多样的通用推理服务
- 需要公平资源分配的多租户环境
- 实时交互应用

### 6.3 资源受限环境 {#63-resource-constrained-environments}

**推荐配置：** `GPU Cache Usage Plugin`

**使用场景：**

- GPU 内存有限的边缘部署
- 成本敏感的云环境
- 多个模型共享 GPU 资源的场景

## 7. 结论 {#7-conclusion}

本文对 Kthena Router ScorePlugin 架构及其性能特性进行了全面分析。实验结果验证了缓存感知调度策略的有效性，尤其是在系统提示较长的场景下，**KVCacheAware Plugin + Least Request Plugin** 组合实现了显著的性能提升。

主要发现包括：

1. **缓存感知策略在长提示场景中提供显著的性能优势**，吞吐量提升可达173%
2. **基于工作负载特征的自适应策略选择**对于实现最佳性能至关重要
3. **可插拔架构**支持针对特定用例的灵活部署配置

未来的工作将重点开发自适应调度算法，能够根据实时工作负载分析和系统状况自动选择最佳插件组合。

---

*文档生成时间：2025年9月9日*
