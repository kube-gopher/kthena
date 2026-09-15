---
sidebar_position: 1
---

# 安装 {#installation}

本指南介绍如何在 Kubernetes 集群中安装 Kthena。

## 前提条件 {#prerequisites}

安装 Kthena 前，请确认满足以下条件：

### 必需条件 {#required-prerequisites}

- **Kubernetes 集群**（1.20 或更高版本）
- 已配置好集群访问权限的 **kubectl**
- **Helm**（3.0 或更高版本）
- 集群管理员权限

### 可选条件 {#optional-prerequisites}

- **[cert-manager](https://cert-manager.io/docs/installation/)**：仅在使用 `cert-manager` 证书管理模式时需要，详见[证书管理](../general/cert-manager.md)。

## 安装方式 {#installation-methods}

### 方式一：使用 Helm 安装（推荐） {#method-1-helm-installation-recommended}

Kthena 的 Helm Chart 发布在 GitHub Container Registry（GHCR）中。

1. **直接从 GHCR 安装 Kthena：**

   ```bash
   helm install kthena oci://ghcr.io/volcano-sh/charts/kthena --version v1.0.0 --namespace kthena-system --create-namespace
   ```

### 方式二：使用 GitHub Release 资源清单手动安装 {#method-2-manual-installation-with-github-release-manifests}

Kthena 将所有必需组件放在一个资源清单文件中，方便从 GitHub Releases 安装。

1. **应用 Kthena 资源清单：**

   ```bash
   kubectl apply --server-side -f https://github.com/volcano-sh/kthena/releases/latest/download/kthena-install.yaml
   ```

   如需安装指定版本，请将 `latest` 替换为相应的发布标签（例如 `v1.2.3`）：

   ```bash
   kubectl apply --server-side -f https://github.com/volcano-sh/kthena/releases/download/vX.Y.Z/kthena-install.yaml
   ```

### 方式三：使用 GitHub Release 中的 Helm 包安装 {#method-3-helm-installation-from-github-release-package}

你也可以从 [GitHub Releases](https://github.com/volcano-sh/kthena/releases) 下载 Helm Chart 包并在本地安装。

1. **下载 Helm Chart 包：**

   下载最新版本：
   ```bash
   curl -L -o kthena.tgz https://github.com/volcano-sh/kthena/releases/latest/download/kthena.tgz
   ```

   下载指定版本（将 `vX.Y.Z` 替换为相应的发布标签）：
   ```bash
   curl -L -o kthena.tgz https://github.com/volcano-sh/kthena/releases/download/vX.Y.Z/kthena.tgz
   ```

2. **使用下载的包安装：**

   ```bash
   helm install kthena kthena.tgz --namespace kthena-system --create-namespace
   ```

## 配置选项 {#configuration-options}

### 按组件安装 {#component-scoped-installation}

Kthena 采用模块化设计：**workload** 控制器和 **networking** 路由器是独立的 Helm 子 Chart，各自拥有独立的 CRD 组，并且在运行时互不依赖。你可以按需安装，安装某个组件时只会引入该组件的 CRD、RBAC 和 Webhook。

| 子 Chart | 组件 | 安装的 CRD |
| --- | --- | --- |
| `workload` | `kthena-controller-manager`（模型生命周期、自动扩缩容） | `ModelServing`、`AutoscalingPolicy` |
| `networking` | `kthena-router`（推理流量路由） | `ModelRoute`、`ModelServer`、`ExternalModelProvider` |

**仅安装工作负载控制器**：使用 Kthena 管理模型工作负载，同时保留现有网关处理流量。

```bash
helm install kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --namespace kthena-system --create-namespace \
  --set networking.enabled=false
```

**仅安装路由器**：将 Kthena Router 作为独立的、感知模型的 LLM 网关，对接由 Deployment、StatefulSet、其他 Operator 管理的 Pod，或外部提供商。

```bash
helm install kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --namespace kthena-system --create-namespace \
  --set workload.enabled=false
```

你可以稍后通过 `helm upgrade --set <subchart>.enabled=true` 启用另一个组件。注意，Helm 升级时不会安装 Chart 的 `crds/` 目录中的文件，因此需要先手动应用新启用组件的 CRD：

```bash
# 1. Fetch and unpack the chart. Replace <chart-version> with the chart version
#    you have installed — `helm list -n kthena-system` shows it.
helm pull oci://ghcr.io/volcano-sh/charts/kthena --version <chart-version> --untar

# 2. Apply the CRDs of the subchart you are enabling: use `networking` for the
#    router, or `workload` for the controllers.
kubectl apply --server-side -f kthena/charts/<subchart>/crds/

# 3. Enable the subchart. This example turns on the router; use
#    `--set workload.enabled=true` to turn on the controllers instead.
helm upgrade kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --namespace kthena-system --reuse-values \
  --set networking.enabled=true
```

### Helm Values {#helm-values}

你可以通过指定参数来自定义安装：

```bash
helm install kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --namespace kthena-system \
  --create-namespace \
  --set workload.controllerManager.replicas=2 \
  --set networking.kthenaRouter.tls.enabled=true
```

### 常用配置参数 {#common-configuration-parameters}

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `workload.enabled` | 安装工作负载控制器及其 CRD | `true` |
| `networking.enabled` | 安装 Kthena Router 及其 CRD | `true` |
| `workload.controllerManager.replicas` | 控制器管理器副本数 | `1` |
| `networking.kthenaRouter.replicas` | 路由器副本数 | `1` |
| `networking.kthenaRouter.tls.enabled` | 为路由器启用 TLS | `false` |
| `global.certManagementMode` | 证书管理模式（`auto`、`cert-manager`、`manual`） | `auto` |

### 完整参数参考 {#full-values-reference}

所有可配置的 Helm 参数请参见 [Helm Chart Values 参考](../reference/helm-chart-values.md)。

## 先升级 CRD，再升级 Helm Release {#upgrade-crds-before-helm}

Helm 会在首次安装时安装 Chart 中的 CRD，但 `helm upgrade` 不会更新它们。升级 Release 前，请先应用目标 Kthena 版本的 CRD：

```bash
helm show crds oci://ghcr.io/volcano-sh/charts/kthena \
  --version vX.Y.Z \
  | kubectl apply --server-side -f -

helm upgrade kthena oci://ghcr.io/volcano-sh/charts/kthena \
  --version vX.Y.Z \
  --namespace kthena-system
```

对于新增 CRD 的版本（包括引入 `ExternalModelProvider` 的版本），必须遵循此顺序。如果 Router 在该 CRD 创建前启动，其 Informer 无法完成初始同步，Router 也就无法开始处理请求。

## 验证安装 {#verification}

安装后，确认所有组件正常运行：

```bash
# Check all pods are running
kubectl get pods -n kthena-system

# Check CRDs are installed (CRDs are included in the main manifest/chart)
kubectl get crd | grep kthena

# Check services
kubectl get svc -n kthena-system
```

## 可选组件 {#optional-components}

### Gang 调度 {#gang-scheduling}

Kthena 使用 **Volcano**（面向 Kubernetes 的高性能批处理系统）提供 Gang 调度能力。

如果需要 Gang 调度，请按照 [Volcano 官方安装指南](https://volcano.sh/en/docs/installation/)安装 Volcano。

### Kthena CLI {#kthena-cli}

Kthena CLI 提供 kubectl 风格的命令，用于管理 Kubernetes 上的 AI 推理工作负载。它支持通过预置模板快速部署，并可选择集成 kubectl-ai，通过自然语言生成命令。

#### 安装 {#installation-1}

从[发布页面](https://github.com/volcano-sh/kthena/releases)下载最新的二进制程序，或从源码构建：

```bash
go install github.com/volcano-sh/kthena/cli/kthena@latest
```

更多信息请参见 [Kthena CLI 文档](../reference/kthena-cli.md)。
