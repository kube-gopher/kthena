---
sidebar_position: 2
---

# 快速入门 {#quick-start}

本指南带你快速部署第一个 AI 模型：从 Hugging Face 安装模型，然后使用简单的 curl 命令执行推理。

Kthena 使用 `ModelServing` 部署可灵活配置的自托管大模型。

## 前提条件 {#prerequisites}

- 已在 Kubernetes 集群中安装 Kthena，详见[安装指南](./installation.md)
- 已配置好 `kubectl`，能够访问 Kubernetes 集群
- Kubernetes 中的 Pod 可以访问互联网
- 已安装 [Volcano](https://volcano.sh/en/docs/installation/)

## ModelServing {#modelserving}

你可以通过 `ModelServing` 灵活配置自托管的大模型。

Model Serving Controller 是 Kthena 的组件，提供灵活且可定制的大模型部署方式。你可以通过 `ModelServing` CRD 配置模型。ModelServing 支持基于角色的大模型部署、Gang 调度和网络拓扑调度，还提供扩缩容和滚动更新等基础功能。

下面的[示例](https://raw.githubusercontent.com/volcano-sh/kthena/refs/heads/main/examples/model-serving/gpu-pd-disaggregation.yaml)使用 `ModelServing` 在 GPU 上部署 Prefill/Decode 分离的 `Qwen/Qwen3-0.6B` 模型。完整步骤及配套的 `ModelServer`、`ModelRoute` 资源请参见[vLLM Prefill-Decode 分离（GPU）](../user-guide/prefill-decode-disaggregation/vllm-pd-disaggregation.md)。

**步骤一：创建 ModelServing 资源**

```sh
kubectl apply -f https://raw.githubusercontent.com/volcano-sh/kthena/refs/heads/main/examples/model-serving/gpu-pd-disaggregation.yaml
```

**步骤二：等待 ModelServing 就绪**

所有待部署的 Pod 启动后，可以通过以下命令查看结果：

```sh
kubectl get pod -owide -l modelserving.volcano.sh/name=vllm-qwen-06b

NAME                              READY   STATUS    RESTARTS   AGE   IP         NODE     NOMINATED NODE   READINESS GATES
vllm-qwen-06b-0-decode-0-0        1/1     Running   0          2m    <pod-ip>   <node>   <none>           <none>
vllm-qwen-06b-0-prefill-0-0       1/1     Running   0          2m    <pod-ip>   <node>   <none>           <none>

------------------------------------------

kubectl get modelserving vllm-qwen-06b -o jsonpath='{.status.conditions}' | jq '.'

[
  {
    "lastTransitionTime": "2025-09-29T08:11:16Z",
    "message": "Some groups is progressing: [0]",
    "reason": "GroupProgressing",
    "status": "False",
    "type": "Progressing"
  },
  {
    "lastTransitionTime": "2025-09-29T08:11:21Z",
    "message": "All Serving groups are ready",
    "reason": "AllGroupsReady",
    "status": "True",
    "type": "Available"
  }
]
```

**步骤三：发送推理请求**

与大模型对话前，请先创建配套的 `ModelServer` 和 `ModelRoute` 资源。具体配置请参见 vLLM GPU PD 指南中的 [ModelServer 配置](../user-guide/prefill-decode-disaggregation/vllm-pd-disaggregation.md#2-modelserver-configuration)和 [ModelRoute 配置](../user-guide/prefill-decode-disaggregation/vllm-pd-disaggregation.md#3-modelroute-configuration)。

随后可以使用以下命令发送请求：

```bash
export MODEL="Qwen/Qwen3-0.6B"
export ROUTER_IP=$(kubectl get svc kthena-router -n kthena-system -o jsonpath='{.spec.clusterIP}')

curl -v http://$ROUTER_IP:80/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"Hello"}]}'
```
