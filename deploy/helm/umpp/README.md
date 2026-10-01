# UMPP Helm Chart

本 Chart 部署 UMPP `control-api`、`dashboard`，并可选用开发依赖与 relay 占位组件。默认使用**外部 PostgreSQL**；内置 PostgreSQL、Redis、NATS 默认关闭。

> **安全要求**：仓库内所有凭据只能是 `CHANGE_ME`/空占位符。生产安装请使用 `existingSecret`、`externalDatabase.passwordSecret` 或不入库的 values 文件。不要 `helm get values` 后回显 Secret，也不要把渲染产物提交到仓库。

## 组件映射

| 组件 | Kubernetes 资源 | 端口 | 说明 |
| --- | --- | --- | --- |
| control-api | Deployment + Service | `8080` API/health/metrics、`8081` 内置反代、`8443` TLS | `/healthz` liveness/readiness；API 镜像来自 `control-plane/Dockerfile` |
| dashboard | Deployment + Service | `8080` | nginx 静态文件，`/api`、`/healthz`、`/metrics` 反代到 control-api；镜像来自 `dashboard/Dockerfile` |
| PostgreSQL | 外部依赖，或开发 StatefulSet + PVC | `5432` | 默认外部；内置版本仅供开发 |
| Redis | 外部依赖，或开发 StatefulSet + PVC | `6379` | 默认外部；生产用 Sentinel/Cluster |
| NATS | 外部依赖，或开发 StatefulSet + PVC | `4222`、监控 `8222` | 默认外部；生产用专用 StatefulSet/Operator |
| relay | DaemonSet | UDP `51820`、`3478` | **wg-easy 占位**；真实数据面是 V2 |

`control-api` 启动时执行 GORM `AutoMigrate`，因此数据库账号需要建表/改表权限。审计、证书、代理配置等主数据保存在 PostgreSQL。

## 前置条件

- Kubernetes 1.25+，Helm 3.16+；默认 Chart 不连接真实集群执行验证。
- 构建并推送 `control-plane/Dockerfile` 与 `dashboard/Dockerfile`，然后设置 `controlApi.image.*`、`dashboard.image.*`。如用私有 registry，设置 `global.imageRegistry` 与 `image.pullSecrets`。
- 默认部署必须先准备 PostgreSQL 16，并在安装 values 中设置 `externalDatabase.host/port/user/database/sslmode/passwordSecret`。
- 如拓扑需要，准备 Redis 7 与 NATS JetStream 2.11。V1 的 control-api 代码没有新增 Redis/NATS 环境变量；Chart 不编造连接变量，只提供外部依赖描述与可选开发 StatefulSet。
- 开发 StatefulSet 需要可用 StorageClass，或显式设置 `*.persistence.storageClass`。`persistence.enabled=false` 时使用 `emptyDir`，重启会丢数据。

## 安装

以下命令在 `deploy/helm` 目录执行。先创建不入库的 `prod-values.yaml`，只放非敏感配置；敏感值使用 Secret。

```bash
helm lint ./umpp --values ./umpp/ci/default-values.yaml
helm upgrade --install umpp ./umpp \
  --namespace umpp --create-namespace \
  --values prod-values.yaml \
  --set externalDatabase.host=db.example.internal \
  --set externalDatabase.user=umpp \
  --set externalDatabase.database=umpp
```

开发依赖全开仅用于测试：

```bash
helm upgrade --install umpp-dev ./umpp \
  --namespace umpp-dev --create-namespace \
  --values ./umpp/ci/default-values.yaml \
  --set postgres.enabled=true --set redis.enabled=true --set nats.enabled=true \
  --set-file secrets.postgresPassword=/run/secrets/postgres-password \
  --set-file secrets.jwtSecret=/run/secrets/jwt-secret \
  --set-file secrets.bootstrapAdminPassword=/run/secrets/admin-password
```

升级前备份 PostgreSQL、TLS 材料与外部 Secret。升级命令与安装相同；Chart 对 ConfigMap/生成 Secret 计算 checksum，配置变化会滚动 Pod。

```bash
helm upgrade umpp ./umpp --namespace umpp --values prod-values.yaml
helm history umpp --namespace umpp
helm rollback umpp <REVISION> --namespace umpp
```

卸载：

```bash
helm uninstall umpp --namespace umpp
```

卸载不会自动删除 StatefulSet 的 PVC；确认备份后再按 PVC 名称清理。外部数据库/Redis/NATS 不受卸载影响。

## Secret 模式

### Chart 生成 Secret

默认 `secrets.create=true`、`secrets.existingSecret=""`。`templates/secret.yaml` 从 values 的 `secrets.*` 读取 `stringData`，提交文件中只允许占位符。必需 key 为：

- `POSTGRES_PASSWORD`
- `UMPP_AUTH_JWT_SECRET`
- `UMPP_BOOTSTRAP_ADMIN_PASSWORD`
- `UMPP_ALERTS_WEBHOOK_URL`（仅 `alerts.webhook.enabled=true`）

可用 `--set-file` 从权限受控的临时文件读取，避免进入 shell history。生成 Secret 的内容参与 Pod checksum；升级时 Secret values 改变会滚动 Pod。

### existingSecret

设置 `secrets.existingSecret=<name>` 后，Chart **不渲染 Secret**，Deployment 只引用该名称。Secret key 可由 `secrets.keys.*` 覆盖。外部数据库密码还可由 `externalDatabase.passwordSecret.name/key` 单独引用。升级时 Chart 通过 `lookup` 读取该 Secret 的 `metadata.resourceVersion` 写入 checksum；因此外部 Secret 轮换后执行 `helm upgrade` 会滚动 Pod。纯 `helm template` 无集群上下文时该 checksum 固定，但不会连接集群。

控制面实现只接受 `UMPP_DATABASE_DSN`，Chart 由 Kubernetes 环境变量引用展开 DSN。因此数据库密码应使用 URL-safe 值（例如 `openssl rand -hex 32`）；若密码包含 `@ : / ? #` 等字符，必须先得到可安全嵌入 DSN 的 Secret 管理方式。

## Ingress 与 TLS：二选一

### cert-manager 终止 Ingress TLS

适合 Dashboard/API 入口。设置 `ingress.enabled=true`、`ingress.className`、`ingress.hosts`，并在 `ingress.annotations` 加入 cert-manager issuer 注解；`ingress.tls` 使用 cert-manager 生成的 Secret：

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  hosts:
    - host: umpp.example.com
      paths:
        - {path: /, pathType: Prefix, service: dashboard}
        - {path: /api, pathType: Prefix, service: controlApi}
  tls:
    - hosts: [umpp.example.com]
      secretName: umpp-ingress-tls
```

### UMPP 自带 ACME

适合 control-api 内置代理管理的域名。设置 `acme.enabled=true`、`acme.agreeTos=true`、`acme.directoryUrl`、`acme.email`、`acme.httpPort`，并设置 `controlApi.proxy.tls.enabled=true`。HTTP-01 由 control-api 的独立 listener 处理，必须把 `acme.httpPort`（生产通常 `80`）和 TLS `8443` 暴露到 CA 可达的入口；可使用 `controlApi.service.type=LoadBalancer` 或四层 Ingress/NodePort 转发。

同一公网域名不要同时让 cert-manager 与 UMPP ACME 写同一 TLS Secret/终止同一入口。`dns-01`、EAB、ACME revoke 在 V1 尚未实现。

## Values 全字段

### 顶层与通用调度

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `nameOverride` | `""` | 覆盖 chart 名称片段 |
| `fullnameOverride` | `""` | 完整资源名前缀 |
| `replicaCount` | `2` | control-api/dashboard Pod 数；HPA 启用时忽略 |
| `image.pullPolicy` | `IfNotPresent` | 所有镜像拉取策略 |
| `image.pullSecrets` | `[]` | imagePullSecret 名称列表 |
| `global.imageRegistry` | `""` | 可选 registry 前缀 |
| `global.commonLabels` | `{}` | 附加到所有资源 |
| `global.commonAnnotations` | `{}` | 附加到所有资源 |
| `podAnnotations` | `{}` | Pod annotation |
| `podLabels` | `{}` | Pod label |
| `nodeSelector` | `{}` | 所有工作负载节点选择 |
| `tolerations` | `[]` | 所有工作负载 toleration |
| `affinity` | `{}` | 所有工作负载 affinity |

### control-api

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `controlApi.image.repository` | `umpp-control-api` | 控制面镜像仓库 |
| `controlApi.image.tag` | `latest` | 控制面镜像 tag |
| `controlApi.service.type` | `ClusterIP` | Service 类型 |
| `controlApi.service.annotations` | `{}` | Service annotation |
| `controlApi.service.ports.api` | `8080` | API/health/metrics |
| `controlApi.service.ports.proxy` | `8081` | 内置 HTTP 反代 |
| `controlApi.service.ports.tls` | `8443` | 内置 TLS 反代 |
| `controlApi.resources.requests` | `100m/128Mi` | CPU/内存请求 |
| `controlApi.resources.limits` | `500m/512Mi` | CPU/内存限制 |
| `controlApi.securityContext` | 非 root、只读根、drop ALL | Pod 容器安全上下文 |
| `controlApi.env` | `[]` | 追加 `name/value/valueFrom` 环境变量 |
| `controlApi.command` | `/usr/local/bin/umpp-api` | 容器命令 |
| `controlApi.args` | `--config /app/configs/config.example.yaml` | 容器参数 |
| `controlApi.auth.accessTtl` | `15m` | access token TTL |
| `controlApi.auth.refreshTtl` | `168h` | refresh token TTL |
| `controlApi.bootstrap.adminEmail` | `admin@example.com` | 初始管理员邮箱 |
| `controlApi.bootstrap.defaultTenant` | `default` | 默认租户 |
| `controlApi.node.heartbeatTimeout` | `60s` | 节点离线阈值 |
| `controlApi.proxy.enabled` | `true` | 启用内置反代 |
| `controlApi.proxy.kind` | `builtin` | `builtin` 或 `nps` |
| `controlApi.proxy.listen` | `:8081` | 反代监听地址 |
| `controlApi.proxy.tls.enabled` | `false` | 启用内置 TLS |
| `controlApi.proxy.tls.listen` | `:8443` | TLS 监听地址 |
| `controlApi.proxy.tls.minVersion` | `1.2` | 最低 TLS 版本 |
| `controlApi.proxy.nps.configPath` | `data/nps/config.json` | NPS 配置文件 |
| `controlApi.proxy.nps.binaryPath` | `/usr/bin/nps` | NPS 二进制 |
| `controlApi.proxy.nps.pidFile` | `data/nps/nps.pid` | NPS PID 文件 |
| `controlApi.proxy.nps.reloadStrategy` | `signal` | NPS reload 策略 |

### dashboard、日志、ACME 与告警

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `dashboard.image.repository` | `umpp-dashboard` | Dashboard 镜像仓库 |
| `dashboard.image.tag` | `latest` | Dashboard 镜像 tag |
| `dashboard.service.type` | `ClusterIP` | Service 类型 |
| `dashboard.service.annotations` | `{}` | Service annotation |
| `dashboard.service.port` | `8080` | nginx 监听端口 |
| `dashboard.resources.requests` | `25m/32Mi` | CPU/内存请求 |
| `dashboard.resources.limits` | `200m/128Mi` | CPU/内存限制 |
| `dashboard.env` | `[]` | 追加环境变量 |
| `log.level` | `info` | `debug/info/warn/error` |
| `log.format` | `json` | `json/text` |
| `acme.enabled` | `false` | 启用 ACME |
| `acme.directoryUrl` | Let's Encrypt v2 | ACME directory |
| `acme.email` | `""` | ACME 账户邮箱 |
| `acme.challenge` | `http-01` | V1 实现 HTTP-01 |
| `acme.httpPort` | `80` | HTTP-01 listener |
| `acme.renewBeforeDays` | `30` | 到期前续期天数 |
| `acme.checkInterval` | `6h` | 续期扫描间隔 |
| `acme.keyType` | `ec256` | `ec256/rsa2048` |
| `acme.agreeTos` | `false` | 必须显式接受 CA TOS |
| `acme.autoRenew` | `true` | 自动续期 |
| `acme.caCertFile` | `""` | 自定义 CA 文件，如 Pebble |
| `alerts.evaluationInterval` | `1m` | 告警评估间隔 |
| `alerts.nodeOfflineAfter` | `5m` | 节点离线阈值 |
| `alerts.certificateExpiringIn` | `720h` | 证书 warning 阈值 |
| `alerts.certificateCriticalIn` | `168h` | 证书 critical 阈值 |
| `alerts.p2pSuccessRateMinimum` | `0.60` | P2P 成功率阈值 |
| `alerts.relaySpikeMultiplier` | `3` | 中继流量突增倍数 |
| `alerts.relayBaselineWindow` | `24h` | 中继基线窗口 |
| `alerts.resolvedRetention` | `168h` | resolved 保留时长 |
| `alerts.webhook.enabled` | `false` | 启用 webhook 通知 |
| `alerts.webhook.url` | `CHANGE_ME` | Secret 中的 webhook URL |
| `alerts.webhook.timeout` | `5s` | webhook 超时 |
| `alerts.webhook.retries` | `3` | webhook 重试次数 |

### Secret 与外部依赖

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `secrets.create` | `true` | 创建 Chart Secret |
| `secrets.existingSecret` | `""` | 非空则不渲染 Secret |
| `secrets.postgresPassword` | `CHANGE_ME` | 生成 Secret 的数据库密码 |
| `secrets.jwtSecret` | `CHANGE_ME` | JWT secret |
| `secrets.bootstrapAdminPassword` | `CHANGE_ME` | 初始管理员密码 |
| `secrets.keys.postgresPassword` | `POSTGRES_PASSWORD` | Secret key |
| `secrets.keys.jwtSecret` | `UMPP_AUTH_JWT_SECRET` | Secret key |
| `secrets.keys.bootstrapAdminPassword` | `UMPP_BOOTSTRAP_ADMIN_PASSWORD` | Secret key |
| `secrets.keys.webhookUrl` | `UMPP_ALERTS_WEBHOOK_URL` | Secret key |
| `externalDatabase.host` | `CHANGE_ME` | 外部 PostgreSQL host |
| `externalDatabase.port` | `5432` | 外部 PostgreSQL port |
| `externalDatabase.user` | `umpp` | 外部 PostgreSQL user |
| `externalDatabase.database` | `umpp` | 外部数据库名 |
| `externalDatabase.sslmode` | `disable` | PostgreSQL DSN sslmode |
| `externalDatabase.passwordSecret.name` | `""` | 空则使用主 Secret |
| `externalDatabase.passwordSecret.key` | `POSTGRES_PASSWORD` | 密码 Secret key |
| `externalRedis.host` | `CHANGE_ME` | 外部 Redis host（运维描述） |
| `externalRedis.port` | `6379` | 外部 Redis port |
| `externalNATS.host` | `CHANGE_ME` | 外部 NATS host（运维描述） |
| `externalNATS.port` | `4222` | 外部 NATS client port |

### 开发依赖与 relay

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `postgres.enabled` | `false` | 开发 PostgreSQL StatefulSet |
| `postgres.image.repository/tag` | `postgres/16` | 开发镜像 |
| `postgres.service.annotations` | `{}` | Service annotation |
| `postgres.service.port` | `5432` | Service 端口 |
| `postgres.database/user` | `umpp/umpp` | 数据库/用户 |
| `postgres.resources.*` | `100m/256Mi` 至 `1/1Gi` | 资源 |
| `postgres.persistence.enabled/size/storageClass` | `true/8Gi/""` | PVC 配置 |
| `redis.enabled` | `false` | 开发 Redis StatefulSet |
| `redis.image.repository/tag` | `redis/7` | 开发镜像 |
| `redis.command` | `redis-server --appendonly yes` | 容器命令 |
| `redis.service.annotations/port` | `{}/6379` | Service |
| `redis.resources.*` | `50m/64Mi` 至 `500m/256Mi` | 资源 |
| `redis.persistence.enabled/size/storageClass` | `true/2Gi/""` | PVC 配置 |
| `nats.enabled` | `false` | 开发 NATS StatefulSet |
| `nats.image.repository/tag` | `nats/2.11-alpine` | 开发镜像 |
| `nats.command` | `-js -m 8222` | JetStream 命令 |
| `nats.service.annotations/clientPort/monitoringPort` | `{}/4222/8222` | Service |
| `nats.resources.*` | `50m/64Mi` 至 `500m/256Mi` | 资源 |
| `nats.persistence.enabled/size/storageClass` | `true/2Gi/""` | PVC 配置 |
| `relay.enabled` | `false` | 启用 wg-easy 占位 DaemonSet |
| `relay.exposure` | `hostNetwork` | `hostNetwork/nodePort` |
| `relay.image.repository/tag` | `ghcr.io/wg-easy/wg-easy/15` | 占位镜像 |
| `relay.service.annotations` | `{}` | Service annotation |
| `relay.service.wireguardPort/turnPort` | `51820/3478` | 容器/Service UDP 端口 |
| `relay.service.wireguardNodePort/turnNodePort` | `0/0` | 0 表示自动分配 NodePort |
| `relay.env` | `INSECURE=true` | 与 compose 占位实现对齐 |
| `relay.resources.*` | `50m/64Mi` 至 `500m/256Mi` | 资源 |

### 入口、弹性与监控

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `ingress.enabled` | `false` | 创建 Ingress |
| `ingress.className` | `""` | IngressClass |
| `ingress.annotations` | `{}` | Ingress annotation |
| `ingress.hosts` | `umpp.example.com` 的 `/`、`/api` | host/path/service 路由 |
| `ingress.tls` | `[]` | `{hosts, secretName}` TLS 列表 |
| `autoscaling.enabled` | `false` | control-api/dashboard HPA |
| `autoscaling.minReplicas/maxReplicas` | `2/10` | HPA 范围 |
| `autoscaling.targetCPUUtilizationPercentage` | `80` | CPU 目标 |
| `podDisruptionBudget.enabled/minAvailable` | `true/1` | 两个 Deployment 的 PDB |
| `serviceAccount.create/name/annotations` | `true/""/{}` | ServiceAccount |
| `networkPolicy.enabled` | `false` | 创建通用 NetworkPolicy |
| `networkPolicy.allowExternalEgress` | `true` | true 时允许全部出站 |
| `networkPolicy.ingress/egress` | `[]/[]` | 追加规则 |
| `prometheus.serviceMonitor.enabled` | `false` | 创建 Prometheus Operator ServiceMonitor |
| `prometheus.serviceMonitor.interval/scrapeTimeout` | `30s/10s` | 抓取参数 |
| `prometheus.serviceMonitor.labels` | `{}` | ServiceMonitor 选择 label |

## 生产注意事项

- 不要启用 `postgres/redis/nats` 的内置单副本 StatefulSet。PostgreSQL 用云数据库或 Operator，Redis 用 Sentinel/Cluster，NATS 用独立 StatefulSet/Operator，并落实备份、监控、故障切换。
- 至少 2 个 control-api/dashboard 副本，启用 HPA/PDB；设置反亲和、资源限制、PodSecurity、NetworkPolicy 与可信 registry。
- PostgreSQL 备份仍使用 `pg_dump`/托管快照，恢复后检查 `/healthz`、`/metrics`、审计日志与证书状态。Chart 不管理数据库备份。
- Prometheus 抓取 `8080/metrics`。V1-R2 中 P2P 成功率、中继吞吐、Agent heartbeat latency 没有真实采集源，数值保持 0；不要据此伪造容量数据。
- ACME HTTP-01、内置代理和 UDP relay 需要独立的 L4 入口规划。云 LoadBalancer 通常不能把 TCP 80/443 和 UDP 51820/3478 合并到同一入口。
- `readOnlyRootFilesystem=true` 依赖 Chart 的 `emptyDir`。若启用 NPS provider，还需自行提供 NPS 二进制、配置和可写路径；默认 builtin 不需要。

## 已知限制

- `relay` 只是对齐 compose 的 wg-easy 占位；它不是 UMPP 中继数据面，UDP `3478` 只是未来 TURN/ICE 预留。
- 没有 PostgreSQL/Redis/NATS Operator 或 CRD 封装，也没有备份/恢复 Operator。
- V1 ACME 仅 HTTP-01；DNS-01、EAB、revoke 未实现。
- 当前 control-api 未消费 Redis/NATS 连接环境变量，Chart 不发明这些变量。
- `verify.sh` 使用 Helm + PyYAML 做离线渲染断言，不连接 Kubernetes；仍需在预发布集群执行 install/upgrade、滚动升级、Ingress、TLS、备份恢复和网络策略验收。
