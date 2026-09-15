---
sidebar_position: 3
---

# 无 GPU 快速入门 {#gpu-free-quick-start}

在**没有 GPU 或 NPU** 的 Kubernetes 集群中体验 Kthena！本指南将部署一个模拟 vLLM API 的推理后端，通过 `ModelServer` 和 `ModelRoute` 暴露服务，再经由 Kthena 路由器发送测试推理请求。

## 前提条件 {#prerequisites}

- 纯 CPU 的 Kubernetes 集群，例如本地 [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) 集群
- 已在 Kubernetes 集群中安装 Kthena，详见[安装指南](./installation.md)
- 已配置好 `kubectl`，能够访问 Kubernetes 集群
- Kubernetes 中的 Pod 可以访问互联网
- 已安装 [Volcano](https://volcano.sh/en/docs/installation/)

## 步骤一：部署模拟推理后端 {#step-1-deploy-the-mock-inference-backend}

仓库提供了一个模拟 vLLM 服务的后端，暴露相同的 OpenAI 兼容 HTTP API 和指标端点，但返回模拟响应，不运行真实模型，因此无需 GPU 或模型权重。

```bash
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/refs/heads/main/examples/kthena-router/LLM-Mock-ds1.5b.yaml
```

等待模拟服务的 Pod 就绪：

```bash
kubectl wait --for=condition=Ready pod -l app=deepseek-r1-1-5b --timeout=180s
kubectl get pods -l app=deepseek-r1-1-5b
```

预期输出：

```text
NAME                                READY   STATUS    RESTARTS   AGE
deepseek-r1-1-5b-xxxxxxxxx-xxxxx    1/1     Running   0          1m
deepseek-r1-1-5b-xxxxxxxxx-xxxxx    1/1     Running   0          1m
deepseek-r1-1-5b-xxxxxxxxx-xxxxx    1/1     Running   0          1m
```

## 步骤二：创建 ModelServer 和 ModelRoute {#step-2-create-the-modelserver-and-modelroute}

`ModelServer` 通过工作负载选择器和端口，告诉路由器哪些 Pod 提供模型服务。`ModelRoute` 声明可路由的模型名称，并将其映射到一个或多个 `ModelServer` 目标。

```bash
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/refs/heads/main/examples/kthena-router/ModelServer-ds1.5b.yaml
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/refs/heads/main/examples/kthena-router/ModelRouteSimple.yaml
```

确认两个资源均已创建：

```bash
kubectl get modelservers,modelroutes
```

预期输出：

```text
NAME                                                              AGE
modelserver.networking.serving.volcano.sh/deepseek-r1-1-5b        1m

NAME                                                              AGE
modelroute.networking.serving.volcano.sh/deepseek-simple          1m
```

## 步骤三：为 Kthena Router 配置端口转发 {#step-3-port-forward-the-kthena-router}

路由器的 Service 类型为 `LoadBalancer`。在没有负载均衡器实现的集群中（例如默认的 Kind 集群），其外部 IP 会一直处于 `<pending>` 状态，因此可以使用端口转发在本地访问：

```bash
kubectl port-forward -n kthena-system svc/kthena-router 8080:80
```

如果本机的 8080 端口已被占用（例如 Podman 默认占用此端口），请改用其他本地端口，例如 `kubectl port-forward -n kthena-system svc/kthena-router 18080:80`，并在下一步使用相应端口。

保持此命令运行，然后在另一个终端中继续操作。

## 步骤四：发送测试推理请求 {#step-4-send-a-test-inference-request}

上述 `ModelRoute` 注册的模型名称为 `deepseek-simple`。通过路由器发送 OpenAI 风格的补全请求（当前模拟镜像要求指定 `max_tokens` 和 `"stream": true`）：

```bash
curl -N http://localhost:8080/v1/completions \
    -H "Content-Type: application/json" \
    -d '{
        "model": "deepseek-simple",
        "prompt": "San Francisco is a",
        "max_tokens": 5,
        "temperature": 0,
        "stream": true
    }'
```

预期响应为包含模拟 Token 的 SSE 数据流，以 `[DONE]` 结束：

```text
data: {"id":"cmpl-4282ab27-171f-4ccf-82a3-adbebc84151a","choices":[{"text":"En","index":0}],"created":1786508011,"model":"deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B","system_fingerprint":null,"object":"text_completion","usage":null}

...

data: {"id":"cmpl-4282ab27-171f-4ccf-82a3-adbebc84151a","choices":[{"text":"","index":0,"finish_reason":"length"}],"created":1786508011,"model":"deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B","system_fingerprint":null,"object":"text_completion","usage":null,"nvext":{"timing":{"request_received_ms":1786508011068,"total_time_ms":54.605582999999996}}}

data: [DONE]
```

虽然请求使用的路由模型名称是 `deepseek-simple`，响应中返回的却是后端模型名称（`deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B`）。这是因为路由器先根据 `ModelRoute` 匹配请求中的 `model` 字段，将其改写为 `ModelServer` 的基础模型名称，再根据 `ModelServer` 的选择器选取就绪的后端 Pod 并转发请求。
