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
| `workload.controllerManager.replicas` | 控制器管理器副本数 | `1` |
| `networking.kthenaRouter.replicas` | 路由器副本数 | `1` |
| `networking.kthenaRouter.tls.enabled` | 为路由器启用 TLS | `false` |
| `global.certManagementMode` | 证书管理模式（`auto`、`cert-manager`、`manual`） | `auto` |

### 完整参数参考 {#full-values-reference}

所有可配置的 Helm 参数请参见 [Helm Chart Values 参考](../reference/helm-chart-values.md)。

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
