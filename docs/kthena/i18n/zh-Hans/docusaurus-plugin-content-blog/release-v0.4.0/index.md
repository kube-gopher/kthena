---
slug: release-v0.4.0
title: "Kthena v0.4.0 发布：更稳健和功能更丰富的版本"
authors: [hzxuzhonghu, LiZhenCheng9527, YaoZengzeng]
tags: [release]
date: 2026-04-14
---

# 宣布 Kthena v0.4.0

感谢过去两个月我们贡献者们的卓越奉献和共同努力，Kthena 的稳定性达到了新的高度。我们要向所有为这一里程碑做出贡献的人表达最深切的感谢。今天，我们非常高兴地宣布 **Kthena v0.4.0** 的正式发布——这是我们迄今为止最强大、功能最丰富的版本！

除了坚如磐石的稳定性外，Kthena v0.4.0 还引入了一波令人兴奋的新功能，旨在简化您的 LLM 工作负载并增强您的 AI 基础设施能力。

<!-- truncate -->

## 改进的可观察性 {#improved-observability}

### 角色状态可见性 {#role-status-visibility}

为了最小化 kube-apiserver 的负载，Kthena 的 `ModelServing` 使用本地存储来缓存 `ServingGroup` 和 `Role` 的状态。虽然效率极高，但我们意识到这在调试过程中限制了我们的可观察性。

在 v0.4.0 版本中，我们打破了黑箱。我们现在[通过 Kubernetes 事件直接暴露角色状态](https://github.com/volcano-sh/kthena/pull/676)，极大地增强了 `ModelServing` 的可观测性。展望未来，我们计划将这一关键的角色信息直接嵌入到 `ModelServing` 状态中，让您对部署拥有完整、透明的控制。

### 全面的访问日志 {#comprehensive-access-logging}

路由器的可观测性现在比以往任何时候都更容易。路由器现在会生成详细的访问日志，为每个请求捕获丰富的[路由元数据](https://github.com/volcano-sh/kthena/pull/621)。下面是一个路由器日志示例：

```sh
[2026-04-16T07:33:08.435627146Z] "POST /v1/chat/completions HTTP/1.1" 200 model_name=deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B model_router=deepseek-r1-1-1.5b model_server=deepseek-r1 selected_pod=deepseek-r1-1-1.5b-6989c66877-p6jvv request_id=ad683d1b-6011-4b0f-b9b5-cbb18d43c57b gateway=dev/default http_route=kthena-e2e-gie-8eoas/llm-route inference_pool=kthena-e2e-gie-8eoas/deepseek-r1-1-1.5b tokens=10/38 timings=3ms(0+2+0)
```

与之前的版本相比，我们引入了 `gateway`、`http_route` 和 `inference_pool` 字段，以提供对网关和网关推理扩展流量的更深层次的可见性。

## 更快、更智能的路由器 {#a-faster-smarter-router}

### 确定性且高效的模型选择 {#deterministic--efficient-model-selection}

以前，将多个`ModelRoute`资源映射到单个模型可能会触发路由冲突——导致规则匹配不明确和目标选择不一致。由于Kubernetes内置的CRD验证无法强制跨对象的全局唯一性，我们在路由层优雅地解决了这一问题。

Kthena v0.4.0引入了强大的[冲突解决机制](https://github.com/volcano-sh/kthena/pull/779)。当存在重复的`ModelRoute`时，路由器会确定性地优先考虑最早的（通常是预构建的）路由，将较新的重复项视为低优先级。每次路由都可预测且稳固可靠。

### 可配置的前缀缓存 {#configurable-prefix-caching}

一个尺寸并不适合所有人。这就是为什么 Kthena 用一个完全[可配置的前缀匹配系统](https://github.com/volcano-sh/kthena/pull/844)取代了硬编码的前缀缓存参数。现在，你可以通过以下参数对前缀缓存行为进行细粒度控制：

- **块大小（用于哈希处理）：** 控制前缀匹配的粒度。块越小匹配越精确，但会增加 CPU 开销，而块越大处理越快。
- **最大块限制：** 设置对给定提示进行哈希处理的上限。这可以在处理过长的输入提示时保护路由器，避免计算瓶颈和延迟峰值。
- **缓存容量：** 定义路由器可以记住的前缀条目数量。增加容量可以提高高多样性工作负载的缓存命中率，但会略微增加内存占用。
- **Top-K 结果：** 决定在找到匹配项时考虑多少候选实例。调整此参数可以实现更好的负载平衡，确保流量在多个节点之间平稳分布，而不是压垮单个活动实例。

通过微调这些设置，您可以根据特定模型和业务 LLM 工作负载定制 Kthena 的路由性能。

## 细粒度、资源高效的滚动更新 {#granular-resource-efficient-rolling-updates}

历史上，Kthena 在整个 `ServingGroup` 级别执行滚动更新。对于大型 LLM 应用程序，完全重建 `ServingGroup` 是一个极其消耗资源且耗时的过程。

为了解决这个问题，我们引入了[基于角色的滚动更新](https://github.com/volcano-sh/kthena/pull/802)。当仅一个特定的`Role`需要更改时，您无需更新整个`ServingGroup`（这也是我们在恢复策略中引入`RoleRecreate`的原因）。从v0.4.0开始，您可以动态调整您的`rolloutStrategy`——大幅降低资源消耗，加快部署速度。

## 开放生态系统 {#an-open-ecosystem}

我们致力于将Kthena建设为一个开放、包容且充满活力的项目，并与更广泛的开源社区共同发展。

### ModelScope支持 {#modelscope-support}

在v0.4.0中，我们扩展了Kthena的模型下载器，支持[ModelScope协议](https://github.com/volcano-sh/kthena/pull/861)。这使用户和运营者能够选择最适合他们需求的模型存储库。

### 支持多种推理引擎 {#support-for-a-variety-of-inference-engines}

此外，我们很自豪地宣布，`ModelServing` 现在已经经过彻底验证，可支持领先的推理引擎，如 **vLLM** 和 **SGLang**。

为了帮助用户快速入门，Kthena v0.4.0 包含了 [SGLang 部署示例和文档](https://github.com/volcano-sh/kthena/pull/883)。本指南使开发者能够轻松在 Kthena 中使用 SGLang。

通过与多种 AI 技术无缝集成，而不是将您锁定在单一解决方案中，Kthena 持续在充满活力的云原生 AI 领域深化发展。我们诚挚邀请全球开发者与我们一起，共同构建这个包容且充满活力的未来！
