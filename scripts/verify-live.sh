#!/bin/bash
# UMPP 独立验证：直连正在运行的栈，只打印状态与计数，绝不回显任何密钥
set -u
cd /home/neil/Documents/Projects/umpp/deploy/docker-compose || exit 1
API=http://127.0.0.1:18080
PW=$(grep -E '^BOOTSTRAP_ADMIN_PASSWORD=' .env | cut -d= -f2-)
EMAIL=$(grep -E '^BOOTSTRAP_ADMIN_EMAIL=' .env | cut -d= -f2-)
echo "== 1. /healthz =="
curl -s "$API/healthz" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("status=%s db=%s version=%s"%(d.get("status"),d.get("db"),d.get("version")))'
echo "== 2. 登录（只报 token 长度，不回显）=="
TOKEN=$(curl -s -XPOST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PW\"}" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("token",""))')
echo "token 长度=${#TOKEN}"
[ -n "$TOKEN" ] || { echo "登录失败"; exit 1; }
AUTH="Authorization: Bearer $TOKEN"
echo "== 3. 节点 =="
curl -s "$API/api/v1/nodes?page_size=50" -H "$AUTH" \
 | python3 -c 'import sys,json;d=json.load(sys.stdin);its=d.get("items",[]);print("total=%s online=%s offline=%s"%(d.get("total"),sum(1 for i in its if i.get("status")=="online"),sum(1 for i in its if i.get("status")=="offline")));[print("   node:",i.get("name"),i.get("status"),i.get("virtual_ip") or "-") for i in its[:5]]'
echo "== 4. 虚拟网络 / 成员 =="
curl -s "$API/api/v1/networks" -H "$AUTH" \
 | python3 -c 'import sys,json;d=json.load(sys.stdin);its=d.get("items",[]);print("networks=%d"%len(its));[print("   ",i.get("name"),i.get("cidr")) for i in its[:5]]'
echo "== 5. 域名 / 代理规则 =="
curl -s "$API/api/v1/domains" -H "$AUTH" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("domains=%d"%len(d.get("items",[])))'
curl -s "$API/api/v1/proxy-rules" -H "$AUTH" | python3 -c 'import sys,json;d=json.load(sys.stdin);its=d.get("items",[]);print("proxy_rules=%d"%len(its));[print("   ",i.get("target_type"),i.get("target"),"enabled=",i.get("enabled")) for i in its[:3]]'
echo "== 6. 中继节点（M4b 新增端点）=="
curl -s "$API/api/v1/relay-servers" -H "$AUTH" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("relay_servers=%d"%len(d.get("items",d) if isinstance(d,dict) else d))' 2>/dev/null || echo "  （端点返回非预期结构）"
echo "== 7. 操作日志（M4b 新增端点）=="
curl -s "$API/api/v1/audit-logs?page_size=5" -H "$AUTH" \
 | python3 -c 'import sys,json;d=json.load(sys.stdin);print("audit total=%s 最近项:"%d.get("total"));[print("   ",i.get("action"),i.get("resource")) for i in d.get("items",[])[:5]]'
echo "== 8. Prometheus 指标 =="
curl -s "$API/metrics" | grep -E '^umpp_(nodes_online|config_version|acl_denied_total|proxy_requests_total)' | head -8
echo "== 9. 内置反向代理端口是否在监听 =="
curl -s -o /dev/null -w "proxy 18081 -> %{http_code}\n" -H 'Host: nonexistent.invalid' "http://127.0.0.1:18081/" || true
echo "== 10. Dashboard（nginx）=="
curl -s -o /dev/null -w "dashboard 13000 -> %{http_code}\n" http://127.0.0.1:13000/
