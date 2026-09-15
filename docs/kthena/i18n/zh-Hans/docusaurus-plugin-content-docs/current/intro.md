---
sidebar_position: 1
---

# Kthena

**Kthena** 是一个轻量级的 Kubernetes 原生 AI 模型服务平台，可将集群转变为企业级推理云。你只需声明所需的模型、流量规则和扩缩容目标，Kthena 控制面就会协调其余工作，无需手动拼接负载均衡器、自动扩缩容器和模型服务器。

Kthena 包含**两个可独立运行的组件**：工作负载控制器和路由器。你可以按需安装其中一个，也可以同时安装两者。

> **声明式 CRD，支持多种引擎，适应不同规模。**
> 组合 `ModelRoute`、`ModelServer`、`ModelServing` 和 `AutoScalingPolicy` 等细粒度资源，可以通过同一套声明式接口管理从单 GPU/NPU 原型到跨节点 Prefill/Decode 分离集群的推理服务。

---

## 为什么选择 Kthena？ {#why-kthena}

| 挑战 | Kthena 的解决方案 |
| --- | --- |
| 平台庞大、依赖复杂 | 两个独立的 Go 二进制程序，依赖少，安装快速，运行成本低，升级简单 |
| 只需要平台的部分能力 | 控制面与数据面完全解耦，可单独部署工作负载控制器或路由器，也可同时部署；两者在运行时互不依赖 |
| 管理多种推理引擎 | 统一的 CRD 层，通过一致的 API 对接 vLLM、SGLang、Triton 和 TorchServe |
| 平衡延迟与吞吐量 | 请求级调度器支持可插拔评分策略，包括 KV 缓存感知、前缀缓存匹配、LoRA 亲和性、最少请求和最低延迟 |
| 以合理成本扩展大模型服务 | Prefill/Decode 分离，支持独立的扩缩容比例和成本感知的自动扩缩容 |
| 在生产环境中安全更新模型 | 支持分区控制的滚动升级、金丝雀发布和自动故障转移 |

---

## 核心功能 {#key-features}

### 轻量化与模块化 {#lightweight--modular}

Kthena 支持按需逐步引入，降低使用和维护成本。

- **资源开销小**：两个独立的 Go 二进制程序，依赖少，安装快速，升级简单。
- **控制面与数据面可独立部署**：**workload** 控制器（`ModelServing`、`AutoscalingPolicy`）与 **networking** 路由器（`ModelRoute`、`ModelServer`）分别作为独立的 Helm 子 Chart，拥有各自的 CRD 组和发布生命周期。
- **运行时无跨平面耦合**：每个组件只与 Kubernetes API 通信，彼此之间不直接调用。你可以让路由器对接由 Deployment 或其他 Operator 管理的工作负载，也可以只运行控制器，通过自己的网关暴露 Pod。
- **扩展能力按需启用**：Gang 调度（Volcano）、Webhook、Gateway API 支持和 TLS 均为可选功能，最小化安装无需引入这些组件。

按组件安装的命令请参见[安装指南](./getting-started/installation.md#component-scoped-installation)。

### 多后端推理引擎 {#multi-backend-inference-engine}

Kthena 通过统一的 Kubernetes 原生 API，将推理引擎作为可插拔后端进行管理。

- **引擎支持**：原生集成 **vLLM**、**SGLang**、**Triton** 和 **TorchServe**，切换引擎无需重写资源清单。
- **服务模式**：既支持标准的多副本服务，也支持在异构加速器（H100、A100、NPU 等）上部署 Prefill/Decode 分离拓扑。
- **智能路由**：可插拔调度器提供过滤和评分插件，支持最少请求、最低延迟、LoRA 亲和性、前缀缓存匹配、KV 缓存感知和 PD 组感知路由，所有策略均在*请求级别*生效。
- **流量管理**：支持加权流量分配的金丝雀发布、基于 Token 的限流、按模型公平排队和自动故障转移策略。
- **LoRA 适配器管理**：动态加载、卸载 LoRA 适配器并路由请求，无需重启 Pod 或排空其中的请求。
- **滚动更新**：通过可配置的分区发布策略，实现模型的零停机升级。

### Prefill-Decode 分离 {#prefill-decode-disaggregation}

大模型推理包含两类差异明显的工作负载：计算密集型的提示词处理（Prefill）和受内存带宽限制的 Token 生成（Decode）。Kthena 可以将它们拆分为独立扩缩容的 `ServingGroup` 角色。

- **工作负载分离**：Prefill 节点专注于计算吞吐量，Decode 节点专注于低延迟，各自拥有独立的副本数和硬件配置。
- **KV 缓存协调**：通过 **LMCache**、**MoonCake** 或 **NIXL** 连接器，在 Prefill 与 Decode Pod 之间传输 KV 缓存，无需应用层自行实现。
- **PD 感知路由**：Kthena Router 能够识别 PD 组，先选择 Decode Pod，再为其匹配同组内兼容的 Prefill Pod，以利用缓存局部性并减少数据传输。

### 成本驱动的自动扩缩容 {#cost-driven-autoscaling}

Kthena 的自动扩缩容不仅考虑指标阈值，还综合考虑成本、服务等级目标（SLO）和异构硬件。

- **多指标扩缩容**：在单个策略中结合自定义指标、CPU、内存、GPU 利用率和预算约束。
- **灵活的策略**：结合稳定扩缩容与应对流量突增的 **panic 模式**，并通过可配置的稳定窗口避免频繁波动。
- **策略绑定**：将自动扩缩容策略绑定到 `ModelServing` 工作负载，并支持根据成本在异构实例池（例如 H100 + A100）之间分配资源。

### 可观测性与监控 {#observability--monitoring}

- **Prometheus 指标**：内置路由延迟（TTFT / TPOT）、队列深度、缓存命中率和各模型吞吐量等指标。
- **请求追踪**：覆盖认证 → 调度 → 代理流程的端到端请求追踪。
- **访问日志**：为每个请求记录结构化日志，包括模型、延迟、Token 数量和上游 Pod。
- **健康检查**：持续对推理 Pod 进行存活、就绪以及引擎专用的健康探测。
