import LightboxImage from '@site/src/components/LightboxImage';
import kthenaRouterArch from '../assets/diagrams/kthena-router-arch.svg';
import kthenaRouterComponents from '../assets/diagrams/kthena-router-components.svg';

# Kthena Router {#kthena-router}

Kthena Router 是独立的路由组件，为大语言模型（LLM）提供统一访问入口，既支持私有部署的大模型，也支持 OpenAI、DeepSeek、HuggingFace 等公共 AI 服务提供商。

我们的目标是提供轻量、易用且可扩展的大模型推理路由器，让用户以较少依赖快速构建并部署生产环境，从而降低维护成本、提高运维效率。

## 概览 {#overview}

<LightboxImage src={kthenaRouterArch} alt="路由器架构"></LightboxImage>

Kthena Router 作为独立的二进制程序部署，可以集成现有网关基础设施，也可以直接作为流量入口，独立处理 AI 工作负载。

它与 Kthena 控制器管理器完全解耦：路由器只需要 `networking.serving.volcano.sh` CRD（`ModelRoute`、`ModelServer`、`ExternalModelProvider`），并通过标签选择器发现后端 Pod。因此，它可以对接由普通 Deployment、StatefulSet、其他 Operator 或 Kthena `ModelServing` 管理的 Pod。使用 `--set workload.enabled=false` 即可单独安装，详见[按组件安装](../getting-started/installation.md#component-scoped-installation)。

对于后端模型访问，路由器既支持外部公共 AI 服务提供商（如 OpenAI、Google Gemini），也支持集群内私有部署的模型。

对于私有部署的模型，路由器支持 vLLM、SGLang 等主流推理框架。通过持续监控模型 Pod 上推理引擎的指标接口，它可以获取实时模型状态，包括已加载的 LoRA 信息、KV 缓存利用率等关键指标，从而进行智能路由，在提高推理吞吐量的同时降低延迟。

## 核心组件 {#core-components}

<LightboxImage src={kthenaRouterComponents} alt="路由器组件"></LightboxImage>

**Router**：核心执行框架，负责请求的接收、处理和转发。

**Listener**：管理 HTTP/HTTPS 监听器，处理指定端口上的入站流量。支持灵活配置不同协议，并绑定多个地址以处理不同类型的请求。

**Controller**：同步和处理 Pod，以及 ModelRoute、ModelServer 等自定义资源（CR）。

**Filters**：包含两个子模块：
- **Auth**：处理流量认证与授权
- **RateLimit**：管理输入 Token、输出 Token 和基于用户等级等多种限流策略

**Backend**：为不同推理引擎提供统一访问抽象，屏蔽各框架在指标接口访问方式和指标命名上的差异。

**Metrics Fetcher**：持续从模型 Pod 上推理引擎的端点收集实时指标，包括 KV 缓存利用率、当前 LoRA 模型状态、请求队列长度和延迟指标（TTFT/TPOT），为智能路由提供最新信息。

**Datastore**：统一的数据存储层，便于访问 ModelServer 与 Pod 的关联关系，以及 Pod 内基础模型、LoRA 配置和运行时指标。

**Scheduler**：实现流量调度算法，由调度框架和多种算法插件组成。框架集成并运行调度插件，对 ModelServer 对应的 Pod 集合进行过滤和评分，选择全局最优 Pod 作为最终访问目标。

初始调度插件包括：
- **最低 KV 缓存使用量**
- **最低延迟**：TPOT（每个输出 Token 的耗时）、TTFT（首 Token 延迟）
- **最少待处理请求**
- **前缀缓存感知**
- **LoRA 亲和性**
- **公平调度**


## 功能 {#features}

**独立二进制程序**：作为独立程序部署，无需作为 Envoy 等现有代理的插件运行，保持轻量、依赖少、易用且部署简单。

**集成现有网关**：兼容基于 Nginx、Envoy 或其他代理的 API 网关基础设施，充分复用认证、授权等已有能力。

**模型感知路由**：利用推理引擎指标优化 AI 流量调度，提高推理性能。

**LoRA 感知负载均衡**：智能地将请求路由到已加载目标 LoRA 适配器的 Pod，将适配器切换延迟从数百毫秒降低到接近零。

**丰富的负载均衡算法**：支持会话亲和性、前缀缓存感知、KV 缓存感知和异构 GPU 硬件感知等算法，以改善推理服务 SLO 并降低成本。

**兼容多种推理引擎**：支持 vLLM、SGLang、TGI 等主流框架。

**基于模型的金丝雀部署与 A/B 测试**：在模型层面支持渐进发布和测试策略。

**认证与授权**：支持 API Key、JWT 和 OAuth 授权等常用方式。

**全面的限流能力**：支持输入 Token、输出 Token 和基于角色等多种限流策略。

**兼容 Kubernetes Gateway API**：兼容 Kubernetes 上游社区 Gateway API 的推理扩展。


## API {#api}

### 1. ModelRoute {#1-modelroute}

ModelRoute 根据模型名称、LoRA 名称、HTTP URL、请求头等请求特征定义流量路由策略，将请求转发到合适的 ModelServer。

### 2. ModelServer {#2-modelserver}

ModelServer 定义推理服务实例和流量访问策略，通过 WorkloadSelector 标识模型所在 Pod，通过 InferenceFramework 指定模型部署使用的推理引擎，并通过 TrafficPolicy 定义访问模型 Pod 的具体策略。
