# 开发环境配置 {#development-setup}

本文档帮助你开始开发 Kthena。如果按照本指南操作时遇到问题，请花一点时间更新此文件。

Kthena 组件仅依赖少量外部工具，构建和运行代码前需要先完成这些工具的配置。

## 配置 Go {#setting-up-go}

Kthena 的所有组件都使用 [Go](https://golang.org) 编写。构建项目需要 Go 开发环境。如果尚未配置，请按照[安装指南](https://golang.org/doc/install)安装 Go 工具。

Kthena 当前使用 Go 1.26.4 构建。

## 配置 Docker {#setting-up-docker}

Kthena 使用 Docker 构建系统创建和发布镜像。为此，需要准备：

- **Docker 平台**：按照[安装指南](https://docs.docker.com/get-docker/)下载并安装 Docker。
- **容器镜像仓库**：GitHub 提供 GitHub Container Registry 镜像服务，可直接通过 GitHub 账户使用；也可以将构建的 Kthena 镜像推送到私有仓库。

## 配置 Kubernetes {#setting-up-kubernetes}

需要支持 CRD 的 Kubernetes 1.28 或更高版本。

如果不确定应选择哪种 Kubernetes 平台，请参见[选择合适的方案](https://kubernetes.io/docs/setup/)。

- [使用 Minikube 安装 Kubernetes](https://kubernetes.io/docs/setup/learning-environment/minikube/)
- [使用 kops 安装 Kubernetes](https://kubernetes.io/docs/setup/production-environment/tools/kops/)
- [使用 kind 安装 Kubernetes](https://kind.sigs.k8s.io/)

## 设置个人访问令牌 {#setting-up-a-personal-access-token}

仅需要向主仓库推送变更的核心贡献者必须完成此步骤。提交拉取请求无需启用双重身份验证，但建议所有贡献者启用这一额外的安全保护。

加入 Volcano 组织需要启用双重身份验证，并设置个人访问令牌以通过 HTTPS 推送。创建令牌的方法请参阅[这些说明](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)。

也可以[添加 SSH 密钥](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/adding-a-new-ssh-key-to-your-github-account)。
