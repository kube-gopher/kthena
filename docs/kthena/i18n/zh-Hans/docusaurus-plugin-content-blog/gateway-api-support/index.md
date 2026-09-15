---
slug: gateway-api-support
title: "Kthena 路由器支持网关 API 和推理扩展"
authors: [YaoZengzeng]
tags: []
date: 2025-12-16
---

# Kthena 路由器支持网关 API 和推理扩展

## 引言 {#introduction}

随着 Kubernetes 成为部署 AI/ML 工作负载的事实标准，对标准化、可互操作的流量管理 API 的需求变得越来越重要。[Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) 代表了传统 Ingress API 的一次重大演进，提供了一种更具表现力、面向角色和可扩展的模型，用于管理 Kubernetes 集群中的南北向流量。

在 Gateway API 的基础上，[Gateway API 推理扩展](https://gateway-api-inference-extension.sigs.k8s.io/) 引入了专门为 AI/ML 推理工作负载设计的资源和功能。该扩展标准化了推理服务通过网关实现暴露和路由的方式，实现了不同网关提供商之间的无缝集成。

Kthena 路由器现在同时支持 Gateway API 和 Gateway API 推理扩展，为用户提供灵活的路由选项，同时保持与行业标准的兼容性。这篇博客文章将探讨这些 API 的重要性、如何启用它们，并展示实际使用示例。

<!-- truncate -->

## 什么是 Gateway API 和 Gateway API 推理扩展？ {#what-is-gateway-api-and-gateway-api-inference-extension}

Gateway API 是一个 Kubernetes 项目，它提供了一个标准化、面向角色的 API 用于管理服务网络。它将关注点分离为不同的角色（基础设施提供者、集群运营者和应用开发者），并支持高级路由功能，包括跨命名空间路由、多协议和流量拆分。Gateway API 推理扩展建立在 Gateway API 之上，为 AI/ML 工作负载提供特定的推理功能。它引入了诸如 InferencePool 和 InferenceObjective 等专用资源，使模型感知路由和 OpenAI API 兼容成为可能，从而实现标准化的推理服务暴露和路由。

## 为什么支持 Gateway API 和推理扩展？ {#why-support-gateway-api-and-inference-extension}

有几个令人信服的理由表明 Kthena Router 应该支持这些 API：

### 1. 解决全局 ModelName 冲突 {#1-resolving-global-modelname-conflicts}

在传统路由配置中，`ModelRoute` 资源中的 `modelName` 字段是全局的。当多个 `ModelRoute` 资源使用相同的 `modelName` 时，会发生冲突，导致路由行为未定义。在多租户环境中，这一限制会成为问题，因为不同的团队或应用可能希望将相同的模型名称用于不同的用途。

Gateway API 通过引入 **Gateway** 资源的概念解决了这个问题，Gateway 资源定义了独立的路由空间。每个 Gateway 可以监听不同的端口，绑定到不同 Gateway 的 ModelRoutes 完全隔离，即便它们使用相同的 `modelName`。这实现了：

- **多租户隔离**：不同团队可以使用相同的模型名称而不会发生冲突
- **环境隔离**：为开发、预发布和生产环境分别配置路由
- **基于端口的路由**：不同的应用可以通过不同的端口访问不同的后端

### 2. 行业标准兼容性 {#2-industry-standard-compatibility}

Gateway API 正在成为 Kubernetes 服务网络的行业标准。通过支持 Gateway API，Kthena Router：

- **提高互操作性**：与其他兼容 Gateway API 的工具和基础设施无缝协作
- **减少厂商锁定**：用户可以更轻松地在不同的网关实现之间迁移
- **利用生态系统**：受益于更广泛的 Gateway API 社区和工具

### 3. 支持 Gateway API 推断扩展 {#3-supporting-gateway-api-inference-extension}

网关 API 推理扩展提供了一种标准化的方法来公开 AI/ML 推理服务。通过支持此扩展，Kthena Router：

- **启用标准化的推理路由**：与 InferencePool 和 InferenceObjective 资源一起工作
- **促进多网关部署**：可以与使用相同 API 的其他网关实现一起工作

### 4. 灵活的部署选项 {#4-flexible-deployment-options}

通过支持网关 API，用户可以在以下选项之间进行选择：

- **原生 ModelRoute/ModelServer**：Kthena 的自定义 CRD，提供高级功能，如 PD 解聚、加权路由和复杂的调度算法
- **网关 API + 推理扩展**：标准 Kubernetes API，提供与其他网关实现的互操作性和兼容性

这种灵活性使用户能够选择最适合其具体需求和基础设施约束的方法。

## 启用网关 API 支持 {#enabling-gateway-api-support}

### 先决条件 {#prerequisites}

在启用网关 API 支持之前，请确保您已：

- 已安装 Kthena 的 Kubernetes 集群（参见[安装指南](/docs/getting-started/installation)）
- 对 Kubernetes 网关 API 概念的基本理解
- `kubectl` 已配置为访问您的集群

### 配置 {#configuration}

在部署 Kthena 路由器时，通过设置 `--enable-gateway-api=true` 参数来启用网关 API 支持：

```bash
# Configure during Helm installation
helm install kthena \
  --set networking.kthenaRouter.gatewayAPI.enabled=true \
  --version v0.2.0 \
  oci://ghcr.io/volcano-sh/charts/kthena
```

或者修改已部署的 Kthena 路由器中的配置：

```bash
kubectl edit deployment kthena-router -n kthena-system
```

确保容器参数包括 `--enable-gateway-api=true`。

### 默认网关 {#default-gateway}

当启用 Gateway API 支持时，Kthena 路由器会自动创建一个具有以下特征的默认网关：

- **姓名**: `default`
- **命名空间**：与 Kthena 路由器的命名空间相同（通常为 `kthena-system`）
- **网关类型**: `kthena-router`
- **监听端口**：Kthena 路由器的默认服务端口（默认值为8080）
- **协议**：HTTP

查看默认网关：

```bash
kubectl get gateway

# Example output:
# NAME      CLASS           ADDRESS   PROGRAMMED   AGE
# default   kthena-router             True         5m
```

## 使用网关 API 与本地 ModelRoute/ModelServer {#using-gateway-api-with-native-modelroutemodelserver}

这个示例演示了如何使用 Gateway API 与 Kthena 的原生 `ModelRoute` 和 `ModelServer` CRD，解决 modelName 冲突问题。

### 第1步：部署模拟模型服务器 {#step-1-deploy-mock-model-servers}

部署模拟 LLM 服务及其对应的 ModelServer 资源：

```bash
# Deploy DeepSeek 1.5B mock service
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/LLM-Mock-ds1.5b.yaml

# Deploy DeepSeek 7B mock service
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/LLM-Mock-ds7b.yaml

# Create ModelServer for DeepSeek 1.5B
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/ModelServer-ds1.5b.yaml

# Create ModelServer for DeepSeek 7B
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/main/examples/kthena-router/ModelServer-ds7b.yaml
```

等待 Pods 就绪：

```bash
kubectl wait --for=condition=ready pod -l app=deepseek-r1-1-5b --timeout=300s
kubectl wait --for=condition=ready pod -l app=deepseek-r1-7b --timeout=300s
```

### 步骤 2：创建一个新的网关 {#step-2-create-a-new-gateway}

创建并应用一个在不同端口监听的新网关：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: kthena-gateway-8081
  namespace: default
spec:
  gatewayClassName: kthena-router
  listeners:
  - name: http
    port: 8081  # Using a different port
    protocol: HTTP
EOF

# Verify Gateway status
kubectl get gateway kthena-gateway-8081 -n default
```

**重要说明**：新创建的网关监听端口 8081，但你需要手动配置 Kthena 路由器的服务以暴露此端口：

```bash
# Edit the kthena-router Service
kubectl edit service kthena-router -n kthena-system
```

在 `spec.ports` 中添加新端口：

```yaml
spec:
  ports:
  - name: http
    port: 80
    targetPort: 8080
    protocol: TCP
  - name: http-81  # Add new port
    port: 81
    targetPort: 8081
    protocol: TCP
```

### 步骤 3：创建绑定到不同网关的 ModelRoutes {#step-3-create-modelroutes-bound-to-different-gateways}

创建并应用一个绑定到默认网关的 ModelRoute：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelRoute
metadata:
  name: deepseek-default-route
  namespace: default
spec:
  modelName: "deepseek-r1"
  parentRefs:
  - name: "default"      # Bind to the default Gateway
    namespace: "kthena-system"
    kind: "Gateway"
  rules:
  - name: "default"
    targetModels:
    - modelServerName: "deepseek-r1-1-5b"  # Backend ModelServer
EOF
```

创建并应用另一个使用**相同 modelName**但绑定到新网关的 ModelRoute：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: networking.serving.volcano.sh/v1alpha1
kind: ModelRoute
metadata:
  name: deepseek-route-8081
  namespace: default
spec:
  modelName: "deepseek-r1"  # Same modelName as the default Gateway's ModelRoute
  parentRefs:
  - name: "kthena-gateway-8081"  # Bind to the new Gateway
    namespace: "default"
    kind: "Gateway"
  rules:
  - name: "default"
    targetModels:
    - modelServerName: "deepseek-r1-7b"  # Using a different backend
EOF
```

**注意**：当启用网关 API 时，`parentRefs` 字段是必需的。没有 `parentRefs` 的 ModelRoutes 将被忽略，并且不会路由任何流量。

### 步骤 4：验证配置 {#step-4-verify-the-configuration}

现在你有两个独立的路由配置：

1. **默认网关（端口 8080）**
   - 模型路由：`deepseek-default-route`
   - 模型名称：`deepseek-r1`
   - 后端：`deepseek-r1-1-5b`（DeepSeek-R1-Distill-Qwen-1.5B）

2. **新网关（端口 8081）**
   - 模型路由：`deepseek-route-8081`
   - 模型名称：`deepseek-r1`（相同的模型名称）
   - 后端：`deepseek-r1-7b`（DeepSeek-R1-Distill-Qwen-7B）

测试默认网关（端口 8080）：

```bash
# Get the kthena-router IP or hostname
ROUTER_IP=$(kubectl get service kthena-router -n kthena-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# If LoadBalancer is not available, use NodePort or port-forward
# kubectl port-forward -n kthena-system service/kthena-router 80:80 81:81

# Test the default port
curl http://${ROUTER_IP}:80/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-r1",
    "prompt": "What is Kubernetes?",
    "max_tokens": 100,
    "temperature": 0
  }'

# Expected output from deepseek-r1-1-5b:
# {"choices":[{"finish_reason":"length","index":0,"logprobs":null,"text":"This is simulated message from deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B!"}],...}
```

测试新网关（端口 8081）：

```bash
# Test port 81
curl http://${ROUTER_IP}:81/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-r1",
    "prompt": "What is Kubernetes?",
    "max_tokens": 100,
    "temperature": 0
  }'

# Expected output from deepseek-r1-7b:
# {"choices":[{"finish_reason":"length","index":0,"logprobs":null,"text":"This is simulated message from deepseek-ai/DeepSeek-R1-Distill-Qwen-7B!"}],...}
```

尽管两个请求都使用相同的 `modelName`（`deepseek-r1`），它们会被路由到不同的后端模型服务，因为它们通过不同的端口访问（对应不同的网关）。这演示了网关 API 如何解决全局模型名称冲突问题。

## 使用带推理扩展的网关 API {#using-gateway-api-with-inference-extension}

此示例演示如何将 Gateway API 推理扩展与 Kthena Router 一起使用，为暴露和路由推理服务提供标准化方法。

### 步骤 1：安装推理扩展 CRDs {#step-1-install-the-inference-extension-crds}

在您的集群中安装 Gateway API 推理扩展 CRDs：

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/latest/download/manifests.yaml
```

### 步骤 2：部署示例模型服务器 {#step-2-deploy-sample-model-server}

部署一个将作为 Gateway 推理扩展后端的模型。请按照 [快速开始](/docs/getting-started/quick-start) 指南，在 `default` 命名空间中部署模型，并确保其处于 `Active` 状态。

部署后，识别模型 Pod 的标签：

```bash
# Get the model pods and their labels
kubectl get pods -l workload.serving.volcano.sh/managed-by=workload.serving.volcano.sh --show-labels

# Example output shows labels like:
# modelserving.volcano.sh/name=demo-backend1
# modelserving.volcano.sh/group-name=demo-backend1-0
# modelserving.volcano.sh/role=leader
# workload.serving.volcano.sh/model-name=demo
# workload.serving.volcano.sh/backend-name=backend1
# workload.serving.volcano.sh/managed-by=workload.serving.volcano.sh
```

### 步骤 3：部署 InferencePool {#step-3-deploy-the-inferencepool}

Kthena 路由器原生支持网关推理扩展，并且不需要端点选择器扩展。创建一个选择您的 Kthena 模型端点的 InferencePool 资源：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: inference.networking.k8s.io/v1
kind: InferencePool
metadata:
  name: kthena-demo
spec:
  targetPorts:
    - number: 8000  # Adjust based on your model server port
  selector:
    matchLabels:
      workload.serving.volcano.sh/model-name: demo
  # Kthena Router natively supports Gateway Inference Extension and does not require the Endpoint Picker Extension.
  # It's just a placeholder for API validation.
  endpointPickerRef:
    name: kthena-demo
    port:
      number: 8000
EOF
```

### 步骤 4：在 Kthena 路由器中启用网关 API 推理扩展 {#step-4-enable-gateway-api-inference-extension-in-kthena-router}

在您的 Kthena 路由器部署中启用网关 API 推理扩展标志：

```bash
kubectl patch deployment kthena-router -n kthena-system --type='json' -p='[
  {
    "op": "add",
    "path": "/spec/template/spec/containers/0/args/-",
    "value": "--enable-gateway-api=true"
  },
  {
    "op": "add",
    "path": "/spec/template/spec/containers/0/args/-",
    "value": "--enable-gateway-api-inference-extension=true"
  }
]'
```

等待部署完成：

```bash
kubectl rollout status deployment/kthena-router -n kthena-system
```

### 步骤 5：部署网关和 HTTPRoute {#step-5-deploy-the-gateway-and-httproute}

创建一个使用 `kthena-router` GatewayClass 的网关资源：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: inference-gateway
spec:
  gatewayClassName: kthena-router
  listeners:
  - name: http
    port: 8080
    protocol: HTTP
EOF
```

创建并应用将网关连接到您的 InferencePool 的 HTTPRoute 配置：

```bash
cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kthena-demo-route
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: inference-gateway
  rules:
  - backendRefs:
    - group: inference.networking.k8s.io
      kind: InferencePool
      name: kthena-demo
    matches:
    - path:
        type: PathPrefix
        value: /
    timeouts:
      request: 300s
EOF
```

### 步骤 6：验证和测试 {#step-6-verify-and-test}

确认网关已分配 IP 地址并报告 `Programmed=True` 状态：

```bash
kubectl get gateway inference-gateway

# Expected output:
# NAME                CLASS           ADDRESS         PROGRAMMED   AGE
# inference-gateway   kthena-router   <GATEWAY_IP>    True         30s
```

验证所有组件是否已正确配置：

```bash
# Check Gateway status
kubectl get gateway inference-gateway -o yaml

# Check HTTPRoute status - should show Accepted=True and ResolvedRefs=True
kubectl get httproute kthena-demo-route -o yaml

# Check InferencePool status
kubectl get inferencepool kthena-demo -o yaml
```

通过网关进行推理测试：

```bash
# Get the kthena-router IP or hostname
ROUTER_IP=$(kubectl get service kthena-router -n kthena-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
# If LoadBalancer is not available, use NodePort or port-forward
# kubectl port-forward -n kthena-system service/kthena-router 80:80

# Test the completions endpoint
curl http://${ROUTER_IP}:80/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen2.5-0.5B-Instruct",
    "prompt": "Write as if you were a critic: San Francisco",
    "max_tokens": 100,
    "temperature": 0
  }'
```

## 本地 ModelRoute/ModelServer：高级功能 {#native-modelroutemodelserver-advanced-features}

虽然 Gateway API 和 Gateway API 推理扩展提供标准化、可互操作的路由能力，但 Kthena 的本地 `ModelRoute` 和 `ModelServer` CRD 提供了更多实验性和高级功能，专门针对 AI/ML 推理工作负载设计：

### 预填充-解码（PD）分离 {#prefill-decode-pd-disaggregation}

本地 ModelRoute/ModelServer 支持 PD 分离，其中计算密集型的预填充阶段与令牌生成解码阶段分开。这可以实现：

- **硬件优化**：为每个阶段使用专用硬件
- **更好的资源利用**：将工作负载特性与硬件能力匹配
- **降低延迟**：独立优化每个阶段

### 基于权重的路由 {#weighted-based-routing}

Native ModelRoute 支持跨多个 ModelServer 的复杂权重路由，实现以下功能：

- **流量分配**：根据权重在后端分配流量
- **A/B 测试**：在不同模型版本之间逐步切换流量
- **基于容量的路由**：根据后端容量和可用性进行路由

这些高级功能使得本地 ModelRoute/ModelServer 成为需要复杂流量管理和优化策略的生产环境的理想选择。然而，Gateway API 和 Gateway API 推理扩展提供了更好的互操作性和与其他网关实现的兼容性，使其适用于多网关部署和标准化基础设施。

## 结论 {#conclusion}

Kthena 路由器对 Gateway API 和 Gateway API 推理扩展的支持为用户提供了灵活的路由选项，能够在标准化和高级功能之间取得平衡。Gateway API 解决了 modelName 冲突问题，并实现了多租户隔离，而 Gateway API 推理扩展提供了标准化的推理路由功能。

用户可以在以下选项中进行选择：

- **网关 API + 推理扩展**：用于标准化、可互操作的路由，适用于不同的网关实现
- **本地 ModelRoute/ModelServer**：用于 PD 拆分、加权路由和复杂调度算法等高级功能

这两种方法都得到充分支持，并且可以在同一个集群中一起使用，为不同的使用场景和需求提供最大的灵活性。

欲了解更多信息，请参阅 [Gateway API 支持指南](/docs/user-guide/gateway-api-support) 和 [Gateway 推理扩展支持指南](/docs/user-guide/gateway-inference-extension-support)。本文中引用的所有示例文件均可在 [kthena/examples/kthena-router](https://github.com/volcano-sh/kthena/tree/main/examples/kthena-router) 目录中找到。
