---
slug: modelserving-blog-post
title: "深入解析 Kthena 的模型服务"
authors: [LiZhenCheng9527]
tags: []
date: 2025-10-14
---

import LightboxImage from '@site/src/components/LightboxImage';
import modelServingArchitecture from './images/modelServing_architecture.svg';
import roleScaling from './images/role_scaling.svg';

# 深入解析 Kthena 的模型服务

## 引言 {#introduction}

随着大模型的参数规模呈指数级增长，单台虚拟机或物理机的资源限制已经无法满足其需求。为了解决这一挑战，业界引入了创新的策略，如PD-解耦部署和大模型与小模型的混合部署。这些方法改变了推理执行方式：不再是单个Pod处理整个推理任务，而是多个Pod经常协作完成一次预测。这种多Pod协作已成为大模型推理部署的一个关键趋势。

在实践中，推理模型仍然可以在单个 Pod 内运行（如传统的单节点场景）、在一组相同的 Pods 中运行（用于更大的模型），或者在具有专门角色的 Pods 之间运行（如 PD 解耦部署）。这种灵活的部署不仅提高了资源利用率，还使大模型推理更加高效。

`ModelServing` 是 `Kthena` 的一个专门组件，用于管理和协调推理模型工作负载的生命周期。由于其三层架构，它可以方便地表示和管理多种部署模型，例如 `PD-disaggregation`、`tensor parallelism`、`pipeline parallelism` 以及原生模型部署。

<!-- truncate -->

## 三层架构 {#three-tier-architecture}

`ModelServing` 采用三层架构 `ModelServing → ServingGroup → Role`，以解决 Kubernetes 传统的两层架构（例如 Deployment 和 StatefulSet）在管理多样化推理工作负载部署场景中的局限性。架构图如下所示：

<LightboxImage src={modelServingArchitecture} alt="ModelServing 架构" />

- **ModelServing:** 中央组件，负责管理推理模型工作负载的生命周期。它提供用于部署和管理推理模型的统一接口，以及查询其状态。
- **ServingGroups:** `ServingGroup` 是 Roles 的集合。每个组可以完成整个推理；它包括用于 PD 解耦部署的 Prefill 和 Decode 角色。
- **角色:** `ServingGroup` 中的每个角色由一组 Pods 组成，这些 Pods 是负责执行推理任务的实际工作负载。每个角色可以分配不同的任务。例如，在 PD-拆分场景中，你可以配置 `prefill role` 和 `decode role`，这非常方便。

关于 `ModelServing` 的定义，请参阅 [modelServing CRD 参考](https://kthena.volcano.sh/docs/next/reference/crd/workload.serving.volcano.sh#modelserving)。

在推理工作负载以组方式管理之后，这种方法与 Volcano 的群组调度和网络拓扑感知调度非常契合。然而，传统云原生工作负载管理所需的基本功能——例如滚动更新和扩缩容——仍然需要额外处理。

### 群组调度 {#gang-scheduling}

帮派调度策略是Volcano-Scheduler的核心调度算法之一。它在调度过程中满足“全有或全无”的调度要求，并避免由于任意调度Pod而导致的集群资源浪费。帮派调度算法的做法是观察已调度的Pod数量是否达到最小运行数。当作业的最小运行数满足时，对作业下的所有Pod执行调度操作；否则，不执行。

在Kthena中，PodGroups是基于ModelServing创建的，通过PodGroups利用Volcano的帮派调度能力。minTaskMember字段指定每个角色中需要帮派调度的Pod数量。

#### 实例级帮派调度 {#instance-level-gang-scheduling}

##### 创建过程 {#creation-process}

为整个 ServingGroup 实例创建一个单独的 PodGroup。

此配置由我们自动生成，无需手动创建。

**PodGroup 配置：**

下面是一个 modelServing 示例：

```yaml
apiVersion: workload.serving.volcano.sh/v1alpha1
kind: ModelServing
metadata:
  name: sample
  namespace: default
spec:
  schedulerName: volcano
  replicas: 1  # servingGroup replicas
  template:
    restartGracePeriodSeconds: 60
    gangPolicy:
      minRoleReplicas:
        prefill: 2
        decode: 2
    roles:
      - name: prefill
        replicas: 4 
        # ... additional role configuration
      - name: decode
        replicas: 4
        # ... additional role configuration
```

```yaml
apiVersion: scheduling.volcano.sh/v1beta1
kind: PodGroup
metadata:
  name: {modelserving-name}-{servinggroup-index}
  namespace: {modelserving-namespace}
  labels:
    modelserving.volcano.sh/name: {modelserving-name}
    modelserving.volcano.sh/group-name: {modelserving-name}-{servinggroup-index}
  annotations:
    scheduling.k8s.io/group-name: {modelserving-name}
spec:
  # MinTaskMember defines minimum pods required for each role replica
  minTaskMember:
    "prefill-0": 2
    "decode-0": 2
    # ... additional role replicas
  minResources:
    # Aggregated resource requirements
  ttlSecondsAfterFinished: {timeout-seconds}
```

**Pod 数量计算：**
如果未配置 `MinRoleReplicas`，则 `minTaskMember` 的值计算如下：

```math
minMember = replicas × Σ(role.replicas × (1 + role.workerReplicas))
```

其中：

- `replicas`：ServingGroup 实例的数量
- `role.replicas`：每个 ServingGroup 中的角色实例数量
- `1 + role.workerReplicas`：每个角色实例的 EntryPod + WorkerPods

如果配置了 `MinRoleReplicas`，则 `minMember` 的值计算如下：

**MinTaskMember 生成逻辑：**

- 对于 MinRoleReplicas map 中指定的每个角色
- 包含从索引 0 到 MinRoleReplicas[roleName] - 1 的角色副本
- 每个角色副本条目的值 = (1 + role.workerReplicas)

**示例配置:**

```yaml
gangPolicy:
  minRoleReplicas:
    prefill: 2    # Require at least 2 prefill role replicas
    decode: 1     # Require at least 1 decode role replica
```

### 滚动更新 {#rolling-update}

滚动更新代表了在线服务实现零停机的重要运营策略。在 LLM 推理服务的上下文中，实施滚动更新对于降低服务不可用的风险非常重要。

目前，`ModelServing` 支持 `ServingGroup` 级别的滚动升级，使用户能够配置 `Partitions` 来控制滚动过程。

- 分区：表示应在何序号对 `ModelServing` 进行分区以进行更新。在滚动更新期间，序号大于或等于 `Partition` 的副本将被更新。序号小于 `Partition` 的副本将不会被更新。

这里是一个配置了逐步发布策略的模型服务:

```yaml
spec:
  rolloutStrategy:
    type: ServingGroupRollingUpdate
    rollingUpdateConfiguration:
      partition: 0
```

在下面，我们将展示 `ModelServing` 四个副本的滚动更新过程。这里模拟了三个副本的状态：

- ✅ 复制品已更新
- ❎ 复制品尚未更新
- ⏳ 副本正在滚动更新中

|        | R-0 | R-1 | R-2 | R-3 | 注意 |
|--------|-----|-----|-----|-----|-------------------------------------------------------------------------------|
| 阶段1 | ❎   | ❎   | ❎   | ❎   | 在滚动更新之前 |
| 第二阶段 | ❎   | ❎   | ❎   | ⏳   | 滚动更新已开始，具有最高序号的副本（R-3）正在更新 |
| 阶段3 | ❎   | ❎   | ⏳   | ✅   | R-3 已更新。下一个副本 (R-2) 现在正在更新中 |
| 阶段4 | ❎   | ⏳   | ✅   | ✅   | R-2 已更新。下一个副本 (R-1) 现在正在更新中 |
| 阶段5 | ⏳   | ✅   | ✅   | ✅   | R-1 已更新。最后一个副本 (R-0) 现在正在更新中 |
| 阶段6 | ✅   | ✅   | ✅   | ✅   | 更新完成。所有副本均已升级到新版本 |

在滚动升级过程中，控制器会删除并重建需要更新的副本中序列号最高的副本。下一个副本在新副本正常运行之前不会更新。

### 扩展 {#scaling}

在云原生基础设施项目中，扩展在资源优化和成本控制中起着至关重要的作用，能提升服务可用性和快速响应，并简化运维管理。

在 modelServing 中，有两层资源描述，分别是 `ServingGroup` 和 `Role`。因此，我们也支持 `ServingGroup level` 和 `role level` 的扩缩容。

在 servingGroup 级别，扩缩容和滚动更新的处理方式类似，在副本集内以相反的顺序应用更新。

角色级别的扩缩容可以对每个角色的副本数量进行精细调整，例如在 PD 解耦部署场景中。这允许动态调整预填或解码副本，使您能够根据各自的工作负载优化 P/D 比例。例如：

1. **长提示、短输出场景**：您可以增加预填副本以处理计算密集型提示处理，同时保持较少的解码副本。

2. **短提示，长输出场景**：您可以增加解码副本以处理顺序令牌生成，同时保持较少的预填充副本。

这种灵活的扩展能力确保根据实际工作负载模式实现最佳资源分配。

当修改 `role.Replicas` 时，会触发角色粒度的扩展。

当触发扩展时，整个 `ServingGroup` 的状态会被设置为扩展状态，然后执行 Pod 的创建或删除过程。

当 Pod 的副本达到预期后，再根据 `ServingGroup` 中所有 Pod 的状态更新 ServingGroup 的状态。

由于角色中的 Pod 承载的是顺序且带标签的任务，所有扩展操作都从最后一个 Pod 开始处理。

#### 角色扩展流程 {#role-scaling-process}

|        | G-0 | G-1 | G-2 | G-3 | 注意 |
|--------|-----|-----|-----|-----|-------------------------------------------------------------------------------|
| 阶段1 | ✅   | ✅   | ✅   | ✅   | 在扩展或缩减之前 |
| 第二阶段 | ❎   | ❎   | ❎   | ⏳   | 开始扩展/缩减，具有最高序号的副本（G-3）开始。在（G-3）中创建/删除角色 |
| 阶段3 | ❎   | ❎   | ⏳   | ✅   | G-3 已扩展。下一个副本（G-2）中的角色正在扩展 |
| 阶段4 | ❎   | ⏳   | ✅   | ✅   | G-2 已扩展。下一个副本（G-1）中的角色正在扩展 |
| 阶段5 | ⏳   | ✅   | ✅   | ✅   | G-1 已扩展。最后一个副本（G-0）中的角色正在扩展 |
| 阶段6 | ✅   | ✅   | ✅   | ✅   | 扩展完成。 |

整体处理流程图如图所示。

<LightboxImage src={roleScaling} alt="Role 扩缩容流程" />

### 重启策略 {#restart-policy}

在 `ModelServing` 中，强调了分组 pod 的概念。因此，当组内的某个 pod 出现错误时，通常会一起重启整个组。然而，在生产环境中，重启整组 pod 可能需要大量的资源和时间。为了解决这个问题，`ModelServing` 还提供了一种重启策略，允许单独重新启动某个 pod。

- **ServingGroupRecreate：** 当组内的 pod 出现错误时，整个组会被重启。
- **RoleRecreate：** 当组内的 pod 出现错误时，组的状态会更新为 `progressing`，并且只重启受影响的 pod。如果 `serviceGroup` 在 `progressing` 状态保持一定时间，整个 servingGroup 将被删除并重新创建。

## 前景 {#prospect}

当前的 ModelServing 通常能够满足模型工作负载管理和调度的基本需求。然而，对 PD 分离场景的支持仍然有限——例如，PD 实例升级等功能尚未完全支持。在未来的发展中，我们计划针对 PD 分离场景引入更多功能。

如果您有兴趣，我们欢迎您加入 Kthena 社区，共同建设我们的开源生态系统。
