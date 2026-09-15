---
slug: release-v0.3.0
title: "Kthena v0.3.0 发布：面向生产的推理编排"
authors: [hzxuzhonghu, LiZhenCheng9527, YaoZengzeng]
tags: [release]
date: 2026-01-31
---

# Kthena v0.3.0 发布：面向生产的推理编排

发布日期：2026-01-31

## 摘要 {#summary}

v0.3.0 版本将 Kthena 建立为一个更强大且可扩展的 AI 推理工作负载平台。该版本在 ModelServing 和 Router 中引入了显著的增强功能。主要亮点包括与 **LeaderWorkerSet** 的无缝集成、针对 PD 解聚的高级 **网络拓扑感知调度** 以及全面的 **Router 可观测性** 框架。此外，该版本带来了原生 **ModelServing 版本控制**、支持 **vLLM 数据并行部署**，以及 Router 的完整端到端测试套件，确保生产环境的高稳定性和可靠性。

<!-- truncate -->

## 新功能 {#whats-new}

### 主要功能概览 {#key-features-overview}

- **LeaderWorkerSet 支持**：与 **LeaderWorkerSet (LWS)** API 的集成允许对分布式推理工作负载进行复杂管理。
- **角色级别的群体调度与拓扑感知**：利用 Volcano 的新功能 `subGroupPolicy`，实现细粒度的**基于角色的群体调度**和**网络拓扑感知**。
- **ModelServing 分区修订控制**：引入了一个原生的基于修订的 ModelServing 版本控制系统。
- **路由器可观测性与调试**：提供全面的文档和路由器可观测性框架，以及专用的调试端口。
- **增强的滚动更新**：对 `maxUnavailable` 的支持允许调整更新速度以实现更快的部署。
- **插件支持**：为 ModelServing 提供灵活的插件架构，可注入自定义配置逻辑。

### ModelServing 角色的 LeaderWorkerSet 支持 {#leaderworkerset-support-for-modelserving-role}

**背景与动机**：
分布式推理工作负载通常需要复杂的拓扑结构，其中一个领导 Pod 管理多个工作 Pod。手动配置这些关系可能容易出错。通过与 Kubernetes LeaderWorkerSet (LWS) API 集成，Kthena 简化了这些工作负载的部署和管理。

**主要功能**：

- **直接集成**：ModelServing 角色现在可以利用 LWS 自动管理领导-工作组。
- **简化拓扑**：降低了定义需要严格协调的分布式推理服务的复杂性。

**相关内容**：

- PR: [#609](https://github.com/volcano-sh/kthena/pull/609), [#683](https://github.com/volcano-sh/kthena/pull/683)
- 贡献者: [@zhiweideren](https://github.com/zhiweideren)

### 角色级群体调度与拓扑感知 {#role-level-gang-scheduling--topology-awareness}

**背景与动机**：
在 Prefill-Decode (PD) 分离场景中，prefill 实例和 decode 实例之间的通信开销至关重要。确保这些实例安排得更接近（例如，在同一个交换机或机架上）可以显著提高性能。Kthena 现在通过利用 Volcano 的 `subGroupPolicy`，实现了对 gang 调度和网络拓扑感知的**精细化、角色级别控制**。

**主要功能**：

- **声明式拓扑策略**：可以在 `ModelServing` 规范中直接为整个 ServingGroup (`groupPolicy`) 和单个角色 (`rolePolicy`) 配置不同的网络拓扑约束。
- **自动 Pod 分组**：控制器会自动为 Pods 打上 `modelserving.volcano.sh/role` 和 `modelserving.volcano.sh/role-id` 标签，从而使 Volcano 能够形成子组，实现精确的拓扑感知式放置。
- **性能优化**：通过将相关任务放置在网络接近的节点上，最小化角色间通信延迟，并最大化带宽利用率，以支持密集的分布式推理作业。
- **角色级别的团体调度**：`subGroupPolicy` 还执行 **角色级别的团体调度**，确保属于特定角色的所有 Pod（例如，所有 `prefill-0` Pod）作为一个原子单元一起调度。这保证了角色不会出现部分部署，这对于分布式推理工作负载的正确性至关重要。

**注意**：此功能需要 Volcano v1.14+ 才能支持 `subGroupPolicy`。

**相关内容**：

- 提案：[网络拓扑](https://github.com/volcano-sh/kthena/blob/main/docs/proposal/network-topology.md)
- PR：[＃587](https://github.com/volcano-sh/kthena/pull/587)
- 贡献者：[ @LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### ModelServing 分区修订控制 {#modelserving-partition-revision-control}

**背景与动机**：
Kthena ModelServing 中的分区字段定义了滚动更新的边界，允许您对更新过程进行分区，这样只有部分 ServingGroups 会被更新，而其他组仍保持在之前的版本。它主要用于金丝雀部署、分阶段发布以及在需要严格控制更新顺序的有状态应用中进行的预发布更新。

**主要功能**：

- **修订跟踪**：自动跟踪 ModelServing 配置的变化。
- **分区保护**：支持基于分区的更新，以确保在发布过程中服务的连续性。
- **回滚**：轻松恢复到之前的稳定修订版本。

**相关内容**：

- PR: [#590](https://github.com/volcano-sh/kthena/pull/590), [#653](https://github.com/volcano-sh/kthena/pull/653), [#671](https://github.com/volcano-sh/kthena/pull/671)
- 贡献者: [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU), [@LiZhenCheng9527](https://github.com/LiZhenCheng9527)

### 路由器可观测性与调试 {#router-observability--debugging}

**背景与动机**：
对推理路由器的深度可视化对于诊断延迟问题和确保服务级别协议（SLA）合规至关重要。新的可观测性框架和调试端口为运维人员提供了必要的工具。

**主要功能**：

- **调试端口**: 一个专用端口（默认 `15000`），用于实时检查路由表和上游健康状态。
- **全面指标**: 提供详细文档和配置，用于监控请求延迟、吞吐量和错误率。
- **端到端测试**: 一个覆盖大多数路由场景的强大端到端测试框架确保了可靠性。

**相关内容**：

- PR: [#599](https://github.com/volcano-sh/kthena/pull/599), [#622](https://github.com/volcano-sh/kthena/pull/622)
- 贡献者: [@yashisrani](https://github.com/yashisrani), [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU)

## 其他显著变化 {#other-notable-changes}

### 功能和改进 {#features-and-improvements}

- **[ModelServing]** 支持在 modelserving 滚动更新中使用 `maxUnavailable` [#640](https://github.com/volcano-sh/kthena/pull/640) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[ModelServing]** 实现扩展插件框架 [#588](https://github.com/volcano-sh/kthena/pull/588) ([@hzxuzhonghu](https://github.com/hzxuzhonghu))
- **[ModelServing]** 支持 vLLM 数据并行部署和专家并行模式
- **[CLI]** 为 PD 解耦用例添加模板 [#571](https://github.com/volcano-sh/kthena/issues/571) ([@huntersman](https://github.com/huntersman))
- **[Client]** 使客户端 QPS 和突发流量可配置 [#686](https://github.com/volcano-sh/kthena/pull/686) ([@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU))
- **[Webhooks]** 在 Helm 图表中默认启用 ModelServing webhooks [#694](https://github.com/volcano-sh/kthena/pull/694) ([@VanderChen](https://github.com/VanderChen))
- **[基础设施]** 通过 `hack/local-up-kthena.sh` 一键从源码部署 [#613](https://github.com/volcano-sh/kthena/pull/613) ([@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU))

### 错误修复 {#bug-fixes}

- **[调度器]** 修复 LeastRequest 评分中的除零错误 [#723](https://github.com/volcano-sh/kthena/pull/723) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[控制器]** 修复角色状态过渡到 Running，以恢复缩容保护 [#706](https://github.com/volcano-sh/kthena/pull/706) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[控制器]** 修复在没有预填充 Pod 可用时 PD 调度器的 panic 问题 [#714](https://github.com/volcano-sh/kthena/pull/714) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[控制器]** 修复 ModelServing 控制器重启后失败 Pod 的静默恢复问题 [#697](https://github.com/volcano-sh/kthena/pull/697) ([@WHOIM1205](https://github.com/WHOIM1205))
- **[控制器]** 修复删除后恢复无头服务的问题 [#598](https://github.com/volcano-sh/kthena/pull/598) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[控制器]** 修复验证 gangpolicy minRoleReplicas 的问题 [#699](https://github.com/volcano-sh/kthena/pull/699) ([@VanderChen](https://github.com/VanderChen))
- **[控制器]** 修复 controllerrevision 数据扭曲问题 [#698](https://github.com/volcano-sh/kthena/pull/698) ([@VanderChen](https://github.com/VanderChen))
- **[控制器]** 修复 modelserving 控制器崩溃 [#688](https://github.com/volcano-sh/kthena/pull/688) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[控制器]** 修复 modelserving 创建时重启导致 Pod 数量不匹配的问题 [#689](https://github.com/volcano-sh/kthena/pull/689) ([@hzxuzhonghu](https://github.com/hzxuzhonghu))
- **[控制器]** 在 ModelServing 验证器中检查 role.Name [#684](https://github.com/volcano-sh/kthena/pull/684) ([@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU))
- **[控制器]** 修复角色删除未触发重建的错误 [#629](https://github.com/volcano-sh/kthena/pull/629) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))
- **[路由器]** 保护由 ModelServing 创建的无头服务 [#598](https://github.com/volcano-sh/kthena/pull/598) ([@LiZhenCheng9527](https://github.com/LiZhenCheng9527))

## 贡献者 {#contributors}

感谢所有使此次发布成为可能的贡献者：

[@hzxuzhonghu](https://github.com/hzxuzhonghu), [@LiZhenCheng9527](https://github.com/LiZhenCheng9527), [@YaoZengzeng](https://github.com/YaoZengzeng), [@git-malu](https://github.com/git-malu), [@FAUST-BENCHOU](https://github.com/FAUST-BENCHOU), [@katara-Jayprakash](https://github.com/katara-Jayprakash), [@zhiweideren](https://github.com/zhiweideren), [@aaradhychinche-alt](https://github.com/aaradhychinche-alt), [@WHOIM1205](https://github.com/WHOIM1205), [@yashisrani](https://github.com/yashisrani), [@huntersman](https://github.com/huntersman), [@VanderChen](https://github.com/VanderChen)
