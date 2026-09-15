---
slug: release-v1.0.0
title: "Kthena v1.0.0 发布：面向生产的 Kubernetes 原生 LLM 服务"
authors: [LiZhenCheng9527]
tags: [release]
date: 2026-06-30
---

## 摘要 {#summary}

我们很高兴地宣布 **Kthena v1.0.0**，这是 Kubernetes 原生 LLM 推理的重要里程碑。此版本着重于服务堆栈的生产就绪性：更准确的 Gateway API 路由、对预填充/解码分离工作负载的一流角色级自动扩缩、更加安全的角色级滚动更新、更好的路由器调度信号、多轮对话工作负载的会话增强、通过 Prometheus 指标和示例仪表板实现更丰富的缓存感知路由器可观测性，以及更完整的命令行体验。

Kthena v1.0.0 还包括一个重要的自动扩缩 API 整合。`AutoscalingPolicyBinding` 已被移除，目标配置现在直接位于 `AutoscalingPolicy` 通过 `homogeneousTarget`、`heterogeneousTarget` 和 `disaggregatedTarget`。

<!-- truncate -->

## 发布亮点 {#release-highlights}

### 主要功能概览 {#key-features-overview}

- **自动扩缩策略的整合与 P/D 协调的分离式自动扩缩:** `AutoscalingPolicyBinding` 已移除，自动扩缩目标配置已整合到 `AutoscalingPolicy`；`disaggregatedTarget` 为预填充/解码工作负载启用角色级协调自动扩缩，允许每个角色根据自身指标进行扩缩，同时通过可选比例约束保持 P/D 副本比率在健康范围内。
- **多轮对话的会话加速:** 路由器可以优先处理最近完成会话的后续请求，从而在并发代理及聊天工作负载下提高重用热 KV 缓存的可能性。
- **路由器调度和可观察性：** 支持每个 Pod 的正在处理请求跟踪、基于 Redis 的跨路由器同步、可配置的 Pod 指标抓取以及缓存感知的 Prometheus 指标，从而提高调度精度和运营可视性。
- **角色级滚动更新可用性控制：** 对 `RoleRollingUpdate` 功能进行了增强。现在每个角色可以设置 `maxUnavailable` 来控制升级节奏。
- **网关 API 和 HTTPRoute 正确性：** Kthena 路由器现在遵循 HTTPRoute 主机名，保持匹配路由规则在后端选择和 URL 重写中的一致性，修复 `PathPrefix` 语义，并遵循网关监听器 `allowedRoutes`。
- **CLI 和 OpenAI 兼容 API 改进：** CLI 现在提供更丰富的状态输出，并支持 `ModelRoute` 和 `ModelServer`，而路由器增加了一个 OpenAI 兼容的 `GET /v1/models` 端点。

### AutoscalingPolicy 合并与 P/D 协调分离自动扩缩 {#autoscalingpolicy-consolidation-and-pd-coordinated-disaggregated-autoscaling}

Kthena v1.0.0 引入了一个更简单且更强大的自动扩缩 API。用户现在可以在单个 `AutoscalingPolicy` 资源中配置需要扩缩的内容、如何收集指标以及扩缩边界。

新的 `disaggregatedTarget` 模式专为基于角色的 ModelServing 部署中的 **协调 P/D 自动扩缩** 设计，特别是预填充/解码分离推理。预填充和解码角色可以根据自己的指标做出扩缩决策，同时自动扩缩器应用共享约束，使两端同步增长和缩减，而不是独立漂移。每个角色可以定义自己的副本范围、指标和指标来源，并且操作员可以配置 `ratioConstraint` 以保持 P/D 副本比例在健康范围内。

示例 `disaggregatedTarget` 配置：

```yaml
spec:
  disaggregatedTarget:
    targetRef:
      apiVersion: workload.serving.volcano.sh/v1alpha1
      kind: ModelServing
      name: vllm-qwen-pd-ms
    roles:
      prefill:
        minReplicas: 1
        maxReplicas: 8
        metrics:
          - name: prefill_waiting_requests
            targetValue: "1"
        metricSources:
          prefill_waiting_requests:
            prometheus:
              serverURL: http://kube-prometheus-stack-prometheus.test.svc.cluster.local:9090
              query: sum(vllm:num_requests_waiting{namespace="autoscale-demo", service="vllm-prefill"})
      decode:
        minReplicas: 1
        maxReplicas: 16
        metrics:
          - name: decode_gpu_cache_usage
            targetValue: "0.75"
        metricSources:
          decode_gpu_cache_usage:
            prometheus:
              serverURL: http://kube-prometheus-stack-prometheus.test.svc.cluster.local:9090
              query: sum(vllm:gpu_cache_usage_perc{namespace="autoscale-demo", service="vllm-decode"})
    ratioConstraint:
      numeratorRole: prefill
      denominatorRole: decode
      minRatio: "0.25"
      maxRatio: "1"
```

相关更改：

- 提议：
  - [提议将 `autoscalingPolicybingding` 合并到 `autoscalingPolicy` #1172](https://github.com/volcano-sh/kthena/pull/1172)
- PRs：
  - [将 autoscalingpolicybinding 合并到 autoscalingpolicy #1203](https://github.com/volcano-sh/kthena/pull/1203)
  - [PD 分解自动扩展器的实现 #1258](https://github.com/volcano-sh/kthena/pull/1258)
- 贡献者：[ @LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### 多轮会话工作负载的会话加速 {#session-boost-for-multi-turn-conversation-workloads}

Kthena v1.0.0 增加了会话加速支持，以改善多轮聊天、智能工作流和 RAG 链，其中每个请求都依赖于先前的响应。在这些工作负载中，后续请求通常会重用大量共享前缀。如果它们在无关流量后等待时间过长，相应的后端 KV 缓存条目可能会被清除，首次生成令牌的时间可能会增加。

会话加速让路由器跟踪最近完成的会话，并在等待队列中优先处理这些会话的后续请求。该实现将此行为与用户公平调度分开，并使用专门的会话加速配置，包括会话头选择、受限的最近会话跟踪、进行中接入限制，以及用于高级缓存命中优化的可选宽限期。

该功能旨在在并发多轮流量下提高缓存重用机会，而不会单独占用 Pod 亲和性。为了获得最强的 KV 缓存效益，运营者还应确保会话感知或缓存感知路由能够将后续请求放置在仍持有热缓存的后端。

Helm 配置示例：

```yaml
networking:
  kthenaRouter:
    sessionBoost:
      enabled: true
      header: X-Session-ID
      maxSessions: 4096
      inflightPerPod: 16
      gracePeriod: 0s
```

相关更改：

- 问题：[改进多轮对话案例 #1190](https://github.com/volcano-sh/kthena/issues/1190)
- PRs：
  - [会话提升队列以优化多轮对话场景 #1183](https://github.com/volcano-sh/kthena/pull/1183)
- 贡献者：[@YaoZengzeng](https://github.com/YaoZengzeng), [@hzxuzhonghu](https://github.com/hzxuzhonghu), [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU), [@LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### 更智能的路由调度和缓存感知可观测性 {#smarter-router-scheduling-and-cache-aware-observability}

路由器现在具有更好的负载信号用于调度决策。Kthena 跟踪每个 Pod 的进行中请求，并可以通过 Redis 在路由器副本之间同步这些计数器，从而允许 `least-request` 插件根据实时负载而不仅仅是本地路由器状态来做出决策。

缓存感知调度也更容易观察。`prefix-cache` 和 `kvcache-aware` 得分插件现在在路由器现有的 `/metrics` 端点上导出 Prometheus 指标，将仅限 klog 的可见性替换为可查询、带模型标签的时间序列，用于负载测试和生产优化。

为了缓存效果，Kthena 记录匹配率直方图而不是简单的命中/未命中计数器。`kthena_router_prefix_cache_match_ratio` 和 `kthena_router_kvcache_aware_match_ratio` 报告提示块已在最佳匹配候选 Pod 上可用的比例，而 `0` 表示实际未命中。这使得命中率可以从 `le="0.0"` 桶中推导，同时也显示了前缀的重用程度。

相关更改：

- 提议：
  - [前缀缓存和 kvcache 感知得分插件的可观察性](https://github.com/volcano-sh/kthena/blob/main/docs/proposal/cache-observability.md)
- PRs：
  - [功能(router): 添加每个 pod 的进行中请求跟踪，并使用 Redis 同步 #962](https://github.com/volcano-sh/kthena/pull/962)
  - [为 KV 缓存感知调度添加 SGLang 分词器支持 #997](https://github.com/volcano-sh/kthena/pull/997)
  - [router: 为前缀缓存和 KV 缓存感知评分插件添加可观测性指标 #1194](https://github.com/volcano-sh/kthena/pull/1194)
  - [功能(router): 使 pod 指标更新间隔可配置 #1151](https://github.com/volcano-sh/kthena/pull/1151)
  - [性能(router): 缓存解析后的提示以避免重复调用 ParsePrompt #1123](https://github.com/volcano-sh/kthena/pull/1123)
  - [修复: 使用有界并发并行化 pod 指标抓取循环 #1255](https://github.com/volcano-sh/kthena/pull/1255)
- 贡献者: [@hzxuzhonghu](https://github.com/hzxuzhonghu), [@blenbot](https://github.com/blenbot), [@kube-gopher](https://github.com/kube-gopher), [@rajnish-jais](https://github.com/rajnish-jais), [@nabrahma](https://github.com/nabrahma)

### 角色 滚动更新 可用性控制 {#role-rollingupdate-availability-control}

Kthena v0.4.0 引入了 `RoleRollingUpdate`，但角色更新仍可能一次性删除 ServingGroup 中的所有过时角色副本。当 `spec.replicas` 是 `1` 时，这可能会在角色级别的滚动更新期间暂时使服务不可用。

Kthena v1.0.0 为 `RoleRollingUpdate` 增加了每个角色的 `maxUnavailable` 支持。操作员现在可以独立控制每个角色更新的步长，使用绝对数量或百分比。这为角色级滚动更新提供了与 ServingGroup 级更新相同的可用性导向控制，同时保持角色级和 ServingGroup 级的发布预算分开。

角色级滚动更新示例配置：

```yaml
spec:
  rolloutStrategy:
    type: RoleRollingUpdate
  template:
    roles:
    - name: prefill
      replicas: 2
      maxUnavailable: 1
      # entryTemplate and workerTemplate omitted
    - name: decode
      replicas: 4
      maxUnavailable: 25%
      # entryTemplate and workerTemplate omitted
```

相关更改：

- 问题：[控制 RoleRollingUpdate 中不可用角色副本的数量 #1188](https://github.com/volcano-sh/kthena/issues/1188)
- PRs：
  - [角色 rollingupdate 支持 maxUnavailable 设置 #1239](https://github.com/volcano-sh/kthena/pull/1239)
- 贡献者: [@hzxuzhonghu](https://github.com/hzxuzhonghu), [@LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### Gateway API 和 HTTPRoute 正确性 {#gateway-api-and-httproute-correctness}

Kthena-router 现在处理 Gateway API 流量时具有更强的正确性保证。路由器遵循 `HTTPRoute.spec.hostnames`，保持与后端选择和 URL 重写过滤器相关联的匹配 HTTPRoute 规则，并在路由中选择更具体的路径规则。这避免了请求意外地通过与匹配请求的规则不同的后端或过滤器路由。

此版本还修复了 Gateway API `PathPrefix` 匹配语义，并使路由器在接受 HTTPRoutes 之前遵循 Gateway 监听器 `allowedRoutes`。

相关更改：

- PRs：
  - [功能: 支持 HTTPRoute 主机名和匹配规则选择 #1174](https://github.com/volcano-sh/kthena/pull/1174)
  - [修复 HTTPRoute PathPrefix 匹配 #1119](https://github.com/volcano-sh/kthena/pull/1119)
  - [修复（路由器）：遵守 Gateway allowedRoutes #1263](https://github.com/volcano-sh/kthena/pull/1263)
- 贡献者: [@zhy76](https://github.com/zhy76), [@Monti-27](https://github.com/Monti-27), [@avinxshKD](https://github.com/avinxshKD)

### CLI 和 OpenAI 兼容 API 改进 {#cli-and-openai-compatible-api-improvements}

`kthena` CLI 现在显示更多有用的状态信息，并支持更多资源类型：

- `kthena get model-servings` 现在显示 `READY` 和 `STATUS` 列。
- 现在支持 `kthena get model-routes` 和 `kthena get model-servers`。
- 现在支持 `kthena describe model-route` 和 `kthena describe model-server`。

路由器还添加了一个兼容 OpenAI 的 `GET /v1/models` 端点，以标准列表响应形式返回可用模型名称。

相关更改：

- PRs：
  - [功能：在 kthena get 输出中添加 STATUS 和 READY 列 #978](https://github.com/volcano-sh/kthena/pull/978)
  - [功能：为 ModelRoute 和 ModelServer 资源添加 CLI 支持 #981](https://github.com/volcano-sh/kthena/pull/981)
  - [功能：支持 /v1/models 端点 #996](https://github.com/volcano-sh/kthena/pull/996)
- 贡献者: [@anirudh240](https://github.com/anirudh240), [@madmecodes](https://github.com/madmecodes)

## 附加增强功能 {#additional-enhancements}

- 通过设置 scale 子资源标签选择器，为 `ModelServing` 添加 KEDA/HPA 兼容性。 [#839](https://github.com/volcano-sh/kthena/pull/839)
- 为 controller-manager 添加调试端口，用于转储缓存的 ServingGroup 和 Role 配置。 [#900](https://github.com/volcano-sh/kthena/pull/900)
- 添加了 SGLang Dynamo 模拟器覆盖和 SGLang 推理模拟器集成。[#920](https://github.com/volcano-sh/kthena/pull/920), [#1231](https://github.com/volcano-sh/kthena/pull/1231)
- 添加了路由器 `pprof` 端点支持。[#1057](https://github.com/volcano-sh/kthena/pull/1057)
- 为 controller-manager 添加了 `debugPort` Helm 图表支持。[#1032](https://github.com/volcano-sh/kthena/pull/1032)
- 添加了 GPU 使用插件端到端覆盖。[#1199](https://github.com/volcano-sh/kthena/pull/1199)
- 刷新了快速入门文档，并建议从 ModelServing 开始。[#1260](https://github.com/volcano-sh/kthena/pull/1260)
- 添加了 DeepSeek-v4 模型服务示例。[#936](https://github.com/volcano-sh/kthena/pull/936), [#937](https://github.com/volcano-sh/kthena/pull/937)
- 添加了 KV-cache 感知调度器插件文档。[#910](https://github.com/volcano-sh/kthena/pull/910)

## 稳定性和正确性亮点 {#stability-and-correctness-highlights}

- **路由器请求处理的正确性：** 在 JWT 认证、空请求验证、流式内容类型解析、重试行为、流中错误传播、IPv6 后端 URL 以及 HTTPRoute 匹配上的修复，使路由器在生产流量下更加可预测。
- **网关 API 对账正确性：** 路由器现在遵循 HTTPRoute 主机名、匹配规则选择、路径前缀语义以及网关监听器命名空间准入规则，从而减少网关 API 配置与运行时路由行为之间的不匹配。
- **自动扩缩器和指标可靠性：** 自动扩缩器指标收集现在优先使用目标引用命名空间，解析指标前检查 HTTP 响应状态，利用最大滑动窗口稳定缩减规模，并支持有界并发 Pod 指标抓取。
- **控制器并发安全性：** ModelServing 控制器修复了数据竞争、并发 map 写入、过时的 Pod 绑定、重复角色删除以及不安全的 PodInfo 访问问题。
- **Webhook 和部署稳定性：** 修正了 webhook 证书生成、cert-manager CA 注入、webhook 名称、验证器消息以及快速入门示例默认值，以减少安装和入门失败。
- **测试和 CI 加固：** 为控制器、自动扩展器、路由器调试模块、SGLang PD 路由以及状态感知缩容行为增加了额外的单元和端到端覆盖。

## 错误修复 {#bug-fixes}

### 路由器 {#router}

- 修复了 JWKS 缓存为空时路由器身份验证的 panic 问题。 [#1219](https://github.com/volcano-sh/kthena/pull/1219)
- JWT 身份验证要求使用 Bearer 方案。 [#1035](https://github.com/volcano-sh/kthena/pull/1035)
- 已拒绝空路由器型号请求。 [#1036](https://github.com/volcano-sh/kthena/pull/1036)
- 在存在参数时修正了流内容类型检测。[#1145](https://github.com/volcano-sh/kthena/pull/1145)
- 修复在聚合代理路径中重试发送空体到备用 Pod 的问题。[#1031](https://github.com/volcano-sh/kthena/pull/1031)
- 从代理请求返回中断和复制错误。[＃1049](https://github.com/volcano-sh/kthena/pull/1049)
- 添加了对 IPv6 pod 后端 URL 的支持。[＃1071](https://github.com/volcano-sh/kthena/pull/1071)
- 修复了过期 KV 缓存所有权。[#1224](https://github.com/volcano-sh/kthena/pull/1224)
- 在 ModelPrefixStore 的 LRU 淘汰回调中移除了不必要的 goroutine。[#1243](https://github.com/volcano-sh/kthena/pull/1243)

### 自动扩缩器和指标 {#autoscaler-and-metrics}

- 在自动扩展器指标收集中首先使用了目标引用的命名空间。[#1068](https://github.com/volcano-sh/kthena/pull/1068)
- 在解析指标响应之前检查了 HTTP 状态。 [#1142](https://github.com/volcano-sh/kthena/pull/1142)
- 使用最大滑动窗口进行自动扩缩器缩减稳定性。[#946](https://github.com/volcano-sh/kthena/pull/946)
- 对 SGLang 指标使用仪表值。[#976](https://github.com/volcano-sh/kthena/pull/976)
- 在最短延迟评分中，将零 TTFT/TPOT 视为未初始化。[#1040](https://github.com/volcano-sh/kthena/pull/1040)
- 使用 ModelServer 配置的工作负载端口进行指标监控，而非硬编码默认值。[#1205](https://github.com/volcano-sh/kthena/pull/1205)

### ModelServing 控制器 {#modelserving-controller}

- 修复了过时的 ModelServer pod 绑定。[#1126](https://github.com/volcano-sh/kthena/pull/1126)
- 修复了错误 pod 处理中的并发 grace-map 访问问题。[#1157](https://github.com/volcano-sh/kthena/pull/1157)
- 防止 PodInfo 访问的数据竞争。 [#1167](https://github.com/volcano-sh/kthena/pull/1167)
- 避免在角色正在删除时重复删除角色。 [#1269](https://github.com/volcano-sh/kthena/pull/1269)
- 修复了 ModelServing webhooks 在副本缺失时的 panic。 [#1055](https://github.com/volcano-sh/kthena/pull/1055)
- 在 ModelServing 拓扑变化时同步 PodGroup 网络拓扑。 [#1088](https://github.com/volcano-sh/kthena/pull/1088)

### 连接器和 PD 路径 {#connectors-and-pd-paths}

- 在每次代理调用时重建 NIXL 预填充/解码请求体。 [#947](https://github.com/volcano-sh/kthena/pull/947)
- 在每次代理调用时重建 SGLang 预填充/解码请求体。 [#984](https://github.com/volcano-sh/kthena/pull/984)
- 增加了流式错误处理改进。 [#1236](https://github.com/volcano-sh/kthena/pull/1236)

### Helm、Webhooks 和示例 {#helm-webhooks-and-examples}

- 修复了 controller-manager webhooks 的 cert-manager CA 注入注解。 [#1018](https://github.com/volcano-sh/kthena/pull/1018)
- 为 webhook 证书使用了随机序列号。 [#1160](https://github.com/volcano-sh/kthena/pull/1160)
- 修正了 webhook 名称和验证器消息。 [#1152](https://github.com/volcano-sh/kthena/pull/1152), [#1159](https://github.com/volcano-sh/kthena/pull/1159)

## API 变更与升级说明 {#api-changes-and-upgrade-notes}

### 重大变更：已移除 AutoscalingPolicyBinding {#breaking-change-autoscalingpolicybinding-removed}

`AutoscalingPolicyBinding` 已从 CRD、生成的客户端、Informer、Lister、应用配置以及 Helm 图表嵌入的 CRD 中移除。

用户应将自动扩缩目标配置迁移到以下 `AutoscalingPolicy.spec` 字段之一：

- `homogeneousTarget`
- `heterogeneousTarget`
- `disaggregatedTarget`

如需更多详情，请参阅 [CRD 文档](https://kthena.volcano.sh/docs/next/reference/crd/workload.serving.volcano.sh) 和 [提案](https://github.com/volcano-sh/kthena/pull/1172)。

### 新的 Router 配置 {#new-router-configuration}

- `METRICS_SCRAPE_INTERVAL` 控制路由器 pod 指标抓取间隔。
- Controller-manager Helm 值现在支持 `debugPort`。

### 构建环境 {#build-environment}

项目工具链、Dockerfile、CI 工作流和开发文档已升级到 Go `1.26.4`。请参见 [feat: comprehensive upgrade to Go 1.26.4 #1244](https://github.com/volcano-sh/kthena/pull/1244)。

## 测试、文档和基础设施 {#tests-docs-and-infrastructure}

Kthena v1.0.0 包含广泛的测试、文档和基础设施改进:

- 为 ModelRouteController 和 GatewayController 添加了单元测试。[＃992](https://github.com/volcano-sh/kthena/pull/992)
- 为状态感知的缩减行为添加了端到端覆盖测试。[＃982](https://github.com/volcano-sh/kthena/pull/982)
- 为 SGLang PD 路由器添加了端到端覆盖测试。[＃994](https://github.com/volcano-sh/kthena/pull/994)
- 添加了自动扩缩器、配置和路由器的调试单元测试。[＃903](https://github.com/volcano-sh/kthena/pull/903)
- 修复了不稳定的控制器和路由器测试。[＃950](https://github.com/volcano-sh/kthena/pull/950), [＃1102](https://github.com/volcano-sh/kthena/pull/1102), [＃1162](https://github.com/volcano-sh/kthena/pull/1162)
- 在列出现有 PodGroups 时使用了 PodGroup 通知器缓存。[＃1081](https://github.com/volcano-sh/kthena/pull/1081)

## 升级说明 {#upgrade-instructions}

在发布后升级到 Kthena v1.0.0：

### 1. 查看重大 API 变更 {#1-review-breaking-api-changes}

在升级之前，请查看集群中现有的自动扩缩容资源。`AutoscalingPolicyBinding` 在 v1.0.0 中已被移除，因此使用旧的双资源自动扩缩容模型的集群必须迁移到新的单资源 `AutoscalingPolicy` 模型。

检查您的集群中是否仍有旧的绑定资源：

```bash
kubectl get autoscalingpolicybindings.workload.serving.volcano.sh --all-namespaces
```

如果返回了任何资源，请在升级之前或升级过程中，将它们的目标和指标源配置迁移到以下 `AutoscalingPolicy.spec` 字段中的一个：

- `homogeneousTarget`
- `heterogeneousTarget`
- `disaggregatedTarget`

对于预填充/解码分离的工作负载，请使用 `disaggregatedTarget.roles`，并在需要时使用 `disaggregatedTarget.ratioConstraint`。

### 2. 备份现有的 Kthena 资源 {#2-back-up-existing-kthena-resources}

在应用新的 CRD 和控制器之前，请备份 Kthena 自定义资源：

```bash
kubectl get modelservings.workload.serving.volcano.sh --all-namespaces -o yaml > modelservings-backup.yaml
kubectl get autoscalingpolicies.workload.serving.volcano.sh --all-namespaces -o yaml > autoscalingpolicies-backup.yaml
kubectl get modelroutes.networking.serving.volcano.sh --all-namespaces -o yaml > modelroutes-backup.yaml
kubectl get modelservers.networking.serving.volcano.sh --all-namespaces -o yaml > modelservers-backup.yaml
```

如果你仍然拥有 `AutoscalingPolicyBinding` 资源，请在移除或迁移它们之前备份：

```bash
kubectl get autoscalingpolicybindings.workload.serving.volcano.sh --all-namespaces -o yaml > autoscalingpolicybindings-backup.yaml
```

### 3. 升级 Kthena {#3-upgrade-kthena}

#### 使用 Helm {#using-helm}

对于基于 OCI 的 GHCR Helm 安装：

```bash
helm upgrade kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --version v1.0.0 \
  --namespace kthena-system
```

如果这是全新安装而非升级：

```bash
helm install kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --version v1.0.0 \
  --namespace kthena-system \
  --create-namespace
```

如果你从发行版 chart 包安装：

```bash
curl -L -o kthena.tgz https://github.com/volcano-sh/kthena/releases/download/v1.0.0/kthena.tgz
helm upgrade kthena kthena.tgz --namespace kthena-system
```

### 4. 验证升级 {#4-verify-the-upgrade}

检查所有 Kthena 组件是否正在运行：

```bash
kubectl get pods -n kthena-system
kubectl get svc -n kthena-system
kubectl get crd | grep serving.volcano.sh
```

验证工作负载、网络和自动扩缩容资源：

```bash
kubectl get modelservings.workload.serving.volcano.sh --all-namespaces
kubectl get autoscalingpolicies.workload.serving.volcano.sh --all-namespaces
kubectl get modelroutes.networking.serving.volcano.sh --all-namespaces
kubectl get modelservers.networking.serving.volcano.sh --all-namespaces
```

### 升级说明 {#upgrade-notes}

- `AutoscalingPolicyBinding` 已被移除。在依赖 v1.0.0 自动扩缩容行为之前，请迁移到 `AutoscalingPolicy.spec.homogeneousTarget`、`spec.heterogeneousTarget` 或 `spec.disaggregatedTarget`。
- `disaggregatedTarget` 支持角色级别自动扩缩容和可选的角色比例约束。固定角色可以使用 `minReplicas == maxReplicas`。
- 需要自定义运行时的 GPU 集群可以设置 `ModelBackend.runtimeClassName`；受污染的 GPU 节点可以使用 `ModelWorker.tolerations` 定位。
- 路由器 Pod 指标抓取可以通过 `METRICS_SCRAPE_INTERVAL` 进行调整。除非需要更新的调度信号或更低的抓取开销，否则保持默认值。
- 开发和构建前提现在使用 Go `1.26.4`；这影响贡献者和镜像构建者，而不影响正常的 Helm 或基于清单的运行时升级。

## 感谢贡献者 {#thank-you-contributors}

感谢所有为 Kthena v1.0.0 做出贡献的人，包括控制器、路由器、自动扩展器、CLI、Helm 图表、示例、文档、CI、生成客户端和测试等方面的贡献者。

特别感谢以下贡献者，包括 [@Abirdcfly](https://github.com/Abirdcfly)、[@Alivestars04](https://github.com/Alivestars04)、[@anirudh240](https://github.com/anirudh240)、[@avinxshKD](https://github.com/avinxshKD)、[@blenbot](https://github.com/blenbot)、[@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU)、[@hzxuzhonghu](https://github.com/hzxuzhonghu)、[@JagjeevanAK](https://github.com/JagjeevanAK)、[@katara-Jayprakash](https://github.com/katara-Jayprakash)、[@kube-gopher](https://github.com/kube-gopher)、[@LiZhenCheng9527](https://github.com/LiZhenCheng9527)、[@madmecodes](https://github.com/madmecodes)、[@nabrahma](https://github.com/nabrahma)、[@nXtCyberNet](https://github.com/nXtCyberNet)、[@rajnish-jais](https://github.com/rajnish-jais)、[@Sanchit2662](https://github.com/Sanchit2662)、[@verma-garv](https://github.com/verma-garv)、[@WHOIM1205](https://github.com/WHOIM1205)、[@xrwang8](https://github.com/xrwang8)、[@zhy76](https://github.com/zhy76) 和 [@YaoZengzeng](https://github.com/YaoZengzeng)。

我们热情邀请开发者、运营者和人工智能基础设施团队试用 Kthena v1.0.0，并共同打造下一代云原生大模型服务。
