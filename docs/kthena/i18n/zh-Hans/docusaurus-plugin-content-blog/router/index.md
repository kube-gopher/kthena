---
slug: router-blog-post
title: "深入探讨 Kthena 路由器"
authors: [YaoZengzeng]
tags: []
date: 2025-10-21
---

import LightboxImage from '@site/src/components/LightboxImage';
import kthenaRouterArchitecture from './images/kthena-router-arch.svg';
import kthenaRouterComponents from './images/kthena-router-components.svg';

# 深入探讨 Kthena 路由器

## 1. 引言 {#1-introduction}

随着大型语言模型（LLMs）在现代应用中变得越来越重要，支持它们的基础设施必须发展以满足高性能、可扩展性和成本要求。在生产环境中部署LLMs带来了独特的挑战：模型资源消耗大，推理工作负载差异显著，用户期望低延迟和高吞吐量。传统的负载均衡器和API网关虽然在常规Web服务中表现出色，但缺乏智能路由AI推理流量所需的感知能力。

**Kthena 路由器** 直接应对这些挑战。它是一个原生 Kubernetes 的独立推理路由器，专为 LLM 服务工作负载构建。不同于通用代理或负载均衡器，Kthena 路由器具备模型感知能力，可根据推理引擎的实时指标做出智能路由决策。这使得复杂的流量管理策略得以实现，从而显著提高吞吐量、降低延迟并减少运营成本。

该路由器无缝集成现有的 API 网关基础设施，同时提供专为 AI 工作负载设计的高级功能：

- **模型感知路由**：利用来自推理引擎（vLLM、SGLang、TGI）的实时指标做出智能路由决策
- **LoRA感知负载均衡**：智能地路由到已加载所需LoRA适配器的Pod，以将适配器切换延迟从数百毫秒降至接近零
- **高级调度算法**：包括前缀缓存感知、KV缓存感知和公平调度等。
- **预填充-解码分离**：原生支持xPyD（x-预填充/y-解码）部署模式

Kthena 路由器以独立二进制文件的形式部署，依赖最少，确保轻量级操作和简单部署。它持续监控推理引擎指标，以获取模型状态的实时信息，包括当前加载的 LoRA 适配器、KV 缓存使用情况、请求队列长度以及延迟指标（TTFT/TPOT）。这种实时感知使路由器能够做出传统负载均衡器无法实现的最佳路由决策。

<!-- truncate -->

## 2. 架构 {#2-architecture}

Kthena 路由器实现了干净、模块化的架构，旨在提高性能和可扩展性。系统由多个核心组件组成，共同提供智能请求路由功能。

<LightboxImage src={kthenaRouterArchitecture} alt="Kthena Router 架构" />

### 2.1 核心组件概述 {#21-core-components-overview}

**路由器**：负责接收、处理和转发请求的核心执行框架。它协调所有其他组件之间的交互，并维护从初始接收到最终响应的请求生命周期。

**监听器**：管理 HTTP/HTTPS 监听器并处理指定端口的传入流量。它为不同协议提供灵活的配置，并可以绑定到多个地址以服务各种类型的请求。监听器确保高效的连接处理，并支持流式和非流式请求模式。

**控制器**：一个原生 Kubernetes 组件，用于同步和处理 Pods 及自定义资源（CRs），如 `ModelRoute` 和 `ModelServer`。该控制器监控集群中的变化，并相应地更新路由器的内部状态，确保路由决策始终基于当前的集群拓扑。

**过滤器**：包含两个关键子模块，在请求到达后端之前进行处理：
- **认证**：处理流量认证和授权，支持 API Key 和 JWT
- **限流**：管理全面的限流策略，包括输入令牌和输出令牌的限制

**后端**：提供一个用于访问各种推理引擎的抽象层。它屏蔽了像 vLLM、SGLang 和 TGI 等框架在指标接口访问方法和指标命名规范上的差异，为调度器提供统一接口。

**指标收集器**：持续收集运行在模型 Pod 上的推理引擎端点的实时指标。它收集的关键性能数据包括：
- KV 缓存使用情况
- 当前 LoRA 模型状态
- 请求队列长度
- 延迟指标（TTFT - 第一令牌时间，TPOT - 每输出令牌时间）

**数据存储**：一个统一的数据存储层，提供对 ModelServer 与 Pod 关联、基础模型/LoRA 配置以及运行时指标的高效访问。它作为所有路由相关信息的中央存储库，并支持实时更新回调。

**调度器**：路由器的大脑，实现复杂的流量调度算法。它由一个调度框架和各种可插拔的调度算法插件组成。该框架整合并运行不同的调度插件，以筛选并评分与 `ModelServers` 对应的 Pod 集合，选择全局最优的 Pod 作为最终访问目标。

<LightboxImage src={kthenaRouterComponents} alt="Kthena Router 组件" />

## 3. 路由器 API {#3-router-api}

Kthena 路由器的路由行为由两个关键的自定义资源定义（CRD）控制：**ModelServer** 和 **ModelRoute**。这些声明式 API 允许你使用熟悉的 Kubernetes 模式定义复杂的路由策略。

### 3.1 ModelRoute {#31-modelroute}

**ModelRoute** 根据请求特征定义流量路由规则。它根据模型名称、LoRA 适配器、HTTP 头和其他条件决定哪些 ModelServer 应处理请求。

关键字段包括：
- **ModelName**：要在传入请求中匹配的模型名称
- **LoRAAdapters**：此路由支持的 LoRA 适配器名称列表
- **Rules**：有序的路由规则列表，每条规则包含：
  - **ModelMatch**：匹配请求的条件（头信息、URI 等）
  - **TargetModels**：要路由到的 ModelServers 列表，可设置可选权重
- **RateLimit**：基于令牌的速率限制配置

有关 `ModelRoute` 的更多详细信息，请参见[定义](https://github.com/volcano-sh/kthena/blob/main/charts/kthena/charts/networking/crds/networking.serving.volcano.sh_modelroutes.yaml)。


### 3.2 ModelServer {#32-modelserver}

**ModelServer** 定义了推理服务实例及其访问策略。它标识运行模型的 pod，指定所使用的推理框架，并定义流量的处理方式。

关键字段包括：
- **WorkloadSelector**：通过标签标识 pod，并支持 PD（Prefill-Decode）组规范
- **Model**：指定服务器托管的基础模型名称
- **InferenceFramework**：指示推理引擎（vLLM、SGLang、TGI 等）
- **WorkloadPort**：定义推理服务监听的端口
- **TrafficPolicy**：配置超时、重试策略及其他流量处理行为
- **KVConnector**：指定 PD 分离部署所使用的 KV 连接器类型（HTTP、Nixl、LMCache、Mooncake）

有关 `ModelServer` 的更多详细信息，请参见[定义](https://github.com/volcano-sh/kthena/blob/main/charts/kthena/charts/networking/crds/networking.serving.volcano.sh_modelservers.yaml)。

### 3.3 示例：基于请求头的多模型路由 {#33-example-header-based-multi-model-routing}

对于分层服务，可根据请求头将用户路由到不同规模的模型：

```yaml
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelRoute
metadata:
  name: deepseek-multi-models
  namespace: default
spec:
  modelName: "deepseek-multi-models"
  rules:
  - name: "premium"
    modelMatch:
      headers:
        user-type:
          exact: premium
    targetModels:
    - modelServerName: "deepseek-r1-7b"
  - name: "default"
    targetModels:
    - modelServerName: "deepseek-r1-1-5b"
---
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelServer
metadata:
  name: deepseek-r1-7b
  namespace: default
spec:
  workloadSelector:
    matchLabels:
      app: deepseek-r1-7b
  workloadPort:
    port: 8000
  model: "deepseek-ai/DeepSeek-R1-Distill-Qwen-7B"
  inferenceEngine: "vLLM"
  trafficPolicy:
    timeout: 10s
---
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelServer
metadata:
  name: deepseek-r1-1-5b
  namespace: default
spec:
  workloadSelector:
    matchLabels:
      app: deepseek-r1-1-5b
  workloadPort:
    port: 8000
  model: "deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B"
  inferenceEngine: "vLLM"
  trafficPolicy:
    timeout: 10s
```

**流量处理流程**：
1. 请求携带模型名称 "deepseek-multi-models" 到达
2. Router 检查是否存在 `user-type: premium` 请求头
3. 高级用户 → 路由到更大的 7B 模型，以获得更好的生成质量
4. 普通用户 → 路由到更小的 1.5B 模型，以提高成本效益

测试高级路由：
```bash
curl http://$ROUTER_IP/v1/completions \
    -H "Content-Type: application/json" \
    -H "user-type: premium" \
    -d '{"model": "deepseek-multi-models", "prompt": "Explain quantum computing"}'
```

此示例演示了 `ModelRoute` 和 `ModelServer` CRD 如何通过标准 Kubernetes API 提供对复杂路由策略的灵活、声明性控制。

## 4. 核心功能 {#4-core-features}

### 4.1 智能调度插件 {#41-intelligent-scheduling-plugins}

真正将 Kthena Router 与传统负载均衡器区分开来的，是其一套支持模型感知的调度插件。这些插件利用实时推理引擎指标来做出智能的路由决策，从而显著提升性能。

#### 4.1.1 前缀缓存感知调度 {#411-prefix-cache-aware-scheduling}

像 vLLM 和 SGLang 这样的现代推理引擎实现了前缀缓存，其中常用的提示前缀会被缓存以避免重复计算。前缀缓存感知插件通过将具有相似前缀的请求路由到相同的节点，从而最大化缓存命中率。

**工作原理**：
- 从传入请求中提取提示前缀
- 维护前缀与已处理它们的节点之间的映射关系
- 将具有匹配前缀的新请求路由到可能已经缓存 KV 状态的节点
- 显著减少重复或相似提示的首次生成时间 (TTFT)

#### 4.1.2 KV 缓存感知调度 {#412-kv-cache-aware-scheduling}

KV Cache Aware插件（`kvcache-aware`）通过基于令牌块的匹配和基于Redis的分布式协调，将请求路由到最有可能拥有匹配KV缓存条目的的Pod。这最大化了缓存命中率，减少了冗余的预填充计算。

**工作原理**：
- Kthena 运行时侧车订阅 vLLM ZMQ kv-events，并将令牌块哈希写入 Redis。
- 路由器会对收到的提示进行标记化，将其划分为固定大小的块，并对每个块进行哈希处理
- Redis被查询以查找哪些Pod缓存了每个令牌块
- Pods的得分基于提示开始的连续块匹配
- 需要与 vLLM Pods 一起部署 Redis 和 Kthena 运行时的 sidecar。

#### 4.1.3 LoRA 亲和性调度 {#413-lora-affinity-scheduling}

LoRA（低秩适配）适配器使模型在无需重新部署基础模型的情况下进行微调。然而，加载和卸载适配器会引入延迟。LoRA Affinity 插件可以将这一开销降到最低。

**工作原理**：
- 跟踪每个 pod 上当前加载的 LoRA 适配器
- 将需要特定 LoRA 的请求路由到已经加载该适配器的 pod
- 如果没有 pod 缓存该适配器，则回退到具有可用适配器槽的 pod
- 将适配器交换延迟从数百毫秒降低到接近零


#### 4.1.4 最低延迟调度 {#414-least-latency-scheduling}

最低延迟插件根据实时延迟指标将请求路由到最快的可用 pod。

**考虑的指标**:
- **TTFT（首次生成标记的时间）**：对于流式响应和用户感知延迟非常重要
- **TPOT（每个输出标记的时间）**：对整体生成速度至关重要

#### 4.1.5 最少请求调度 {#415-least-request-scheduling}

最少请求插件会考虑等待处理的请求数量和正在运行的请求数量，以路由到最不繁忙的 pod。

**工作原理**：
- 监控推理引擎的 `num_requests_running` 和 `num_requests_waiting` 指标
- 计算每个 pod 的总待处理工作量
- 将新请求路由到最不繁忙的 pod
- 防止热点问题，并确保负载均匀分布

#### 4.1.6 插件配置 {#416-plugin-configurations}

这些插件通过调度器框架协同工作。您可以通过路由器配置设置启用的插件及其相对权重。

调度器框架按顺序运行已启用的插件：
1. **过滤**：插件会剔除不合适的 Pod（例如，缓存不足、LoRA 不匹配）
2. **评分**：插件根据其标准对剩余 Pod 进行评分
3. **选择**：选择得分最高的 Pod 来处理请求

这种可组合的架构允许您根据特定工作负载需求定制路由行为。

### 4.2 公平调度 {#42-fairness-scheduling}

公平调度确保基于令牌消耗历史在用户之间进行公平的资源分配。

**工作原理**：
- 跟踪每个用户每个模型的累计令牌使用量（输入 + 输出）
- 根据历史使用情况反比例地分配请求优先级
- 将请求排队并按优先顺序处理
- 防止任何单个用户垄断资源

**使用场景**：
- 具有共享基础设施的多租户平台
- 具有公平分配策略的研究集群
- 需要基于使用量限流的SLA驱动系统

### 4.3 预填充-解码分解支持 {#43-prefill-decode-disaggregation-support}

对于高级部署模式，Kthena Router 原生支持预填充-解码分解（xPyD），其中计算密集型的预填充阶段与令牌生成解码阶段分离。

**工作原理**：
- 从 ModelServer CRD 中识别 PD 组配置
- 路由将预填充请求发送到经过预填充优化的 Pods
- 通过可配置连接器（HTTP、Redis 等）传输 KV 缓存状态
- 路由将解码请求发送到经过解码优化的 Pods
- 透明地协调客户端的两阶段处理过程

**优势**：
- 通过将工作负载特性与硬件匹配，优化硬件利用率
- 通过为每个阶段使用专用硬件，减少延迟
- 通过更好的资源分配，提高成本效率

### 4.4 基于令牌的速率限制 {#44-token-based-rate-limiting}

Kthena Router 提供全面的速率限制功能，以保护您的推理基础设施免受过载影响，并确保用户之间公平的资源分配。

- **输入令牌限制**：控制每个用户或 API 密钥的输入提示令牌速率
- **输出令牌限制**：限制生成的令牌以管理计算成本
- **本地速率限制**：在每个路由器实例基础上执行限制。
- **全局速率限制**：在所有路由器实例之间执行共享限制，使用像 Redis 这样的中央存储。

### 4.5 可观测性 {#45-observability}

Kthena Router 提供面向生产 LLM 服务的全面可观测性功能：

- **指标**：公开详细指标，包括请求延迟、令牌消耗、调度器插件性能和速率限制统计信息，位于 `/metrics` 端点
- **结构化访问日志**：记录完整的请求生命周期，包括路由决策、时间分解和令牌跟踪，以 JSON 或文本格式
- **调试端点**：提供 `/debug/config_dump/*` API，用于检查内部状态、ModelRoute/ModelServer 配置以及实时 Pod 指标
- **标准集成**：可与 Prometheus、Grafana、ELK 及其他可观测性堆栈无缝协作，用于监控、告警和故障排除


## 5. 性能 {#5-performance}

**Kthena Router** 中的 **ScorePlugin** 模块利用可配置的、可插拔的架构，实现对推理请求的多维打分和智能路由。为了展示智能调度的影响，我们基于 **DeepSeek-R1-Distill-Qwen-7B** 模型构建了标准化基准测试环境，以评估不同调度策略在长、短系统提示场景下的性能。

实验结果表明，在**长系统提示场景**下，**KVCacheAware 插件 + Least Request 插件**组合实现了**2.73 倍更高的吞吐量**，并将**TTFT 延迟降低了 73.5%**，显著优化了整体推理服务性能，并验证了面向缓存调度在大规模模型推理中的核心价值。

### 5.1 实验设置 {#51-experimental-setup}

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

### 5.2 长系统提示场景（4096 个标记） {#52-long-system-prompt-scenario-4096-tokens}

**表 2：性能指标 – 长提示**

| 插件配置 | 运行次数 | 成功率（%） | 吞吐量（请求/秒） | 延迟（秒） | TTFT（秒） |
| :--------------------------- | :---: | :--------------: | :----------------: | :---------: | :------: |
| 最少请求 + KVCacheAware |   3   |      100.0       |     **32.22**      |  **9.22**   | **0.57** |
| 最少请求 + 前缀缓存 |   3   |      100.0       |       23.87        |    12.47    |   0.83   |
| 随机 |   3   |      100.0       |       11.81        |    25.23    |   2.15   |
| 最少请求 |   3   |      100.0       |        9.86        |    30.13    |  12.46   |
| GPU 使用率 |   3   |      100.0       |        9.56        |    30.92    |  13.14   |
| 最少延迟 |   3   |      100.0       |        9.47        |    31.44    |  11.07   |

## 6. 结论 {#6-conclusion}

Kthena 路由器代表了 LLM 服务基础设施的重大进步。通过超越简单的负载均衡，转向模型感知、基于指标的路由，它释放了以前无法实现的显著性能提升和成本节约。

它是开源的，并且今天即可使用。[文档](https://volcano-sh.github.io/kthena/)提供了关于安装、配置和部署的全面指南。[示例目录](https://github.com/volcano-sh/kthena/tree/main/examples/kthena-router)包含常见场景的可直接使用配置。

无论您是在运行单个模型还是管理复杂的多租户大型语言模型平台，Kthena Router 都提供了实现最大化性能、最小化成本和提供卓越用户体验所需的智能路由能力。
