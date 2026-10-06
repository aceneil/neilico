#!/usr/bin/env python3
"""账号管理真机复验：轮换密码 → 校验 env 回写 → 改回原密码 → 校验一致性。
全程不打印任何明文，只用 sha256 前 10 位指纹比对。带兜底：无论如何都把密码恢复成 env 原值。"""
import hashlib
import json
import os
import subprocess

BASE = "http://192.168.123.90:13000"
ENVF = "/home/neil/Documents/Docker/data/neilico/neilico.env"


def sh(*args):
    return subprocess.run(args, capture_output=True, text=True).stdout


def envv(key):
    out = sh("grep", "-m1", "^" + key + "=", ENVF).strip().split("=", 1)
    return out[-1] if len(out) > 1 else ""


def fp(value):
    return hashlib.sha256(value.encode()).hexdigest()[:10]


def login(password, email):
    raw = sh("curl", "-s", "-X", "POST", BASE + "/api/v1/auth/login",
             "-H", "Content-Type: application/json",
             "--data", json.dumps({"email": email, "password": password}))
    try:
        return json.loads(raw).get("token")
    except Exception:
        return None


def rotate(current, new, token):
    return sh("curl", "-s", "-o", "/tmp/rotate.json", "-w", "%{http_code}",
              "-X", "POST", BASE + "/api/v1/account/password/rotate",
              "-H", "Authorization: Bearer " + token,
              "-H", "Content-Type: application/json",
              "--data", json.dumps({"current_password": current, "new_password": new})).strip()


def main():
    email, original = envv("NEILICO_BOOTSTRAP_ADMIN_EMAIL"), envv("NEILICO_BOOTSTRAP_ADMIN_PASSWORD")
    print(f"  healthz: {sh('curl', '-s', '--max-time', '5', BASE + '/healthz')}")
    print("  原密码登录:", "200 ✓" if login(original, email) else "✗")

    token = login(original, email)
    temporary = "TmpR-%s%s" % (os.urandom(6).hex(), os.urandom(6).hex())
    restored = False
    try:
        if token:
            code = rotate(original, temporary, token)
            body = sh("cat", "/tmp/rotate.json").replace(" ", "")
            after = envv("NEILICO_BOOTSTRAP_ADMIN_PASSWORD")
            wrote_env = 'env_file_updated":true' in body
            print(f"  ★★轮换 → HTTP {code} | env_file_updated=true? {'✓✓' if wrote_env else '✗ ' + body[:60]}")
            print(f"  ★★env 回写成新密码: {'✓✓' if fp(after) == fp(temporary) else '✗'}")
            token2 = login(temporary, email)
            print("  新密码登录:", "200 ✓" if token2 else "✗")
            if token2:
                code3 = rotate(temporary, original, token2)
                back = envv("NEILICO_BOOTSTRAP_ADMIN_PASSWORD")
                restored = fp(back) == fp(original)
                print(f"  ★★改回原密码 → HTTP {code3} | env 恢复原值: {'✓✓' if restored else '✗'}")
    finally:
        if not restored and login(temporary, email):
            rotate(temporary, original, login(temporary, email))
            print("  ⚠ 已兜底恢复")
        ok = bool(login(original, email))
        same = fp(envv("NEILICO_BOOTSTRAP_ADMIN_PASSWORD")) == fp(original)
        print(f"  最终：原密码可登录 {'✓' if ok else '✗'} | env 指纹 {'一致 ✓' if same else '不一致 ✗'}")

    print("  helper 可读:", "✓" if sh("bash", "/home/neil/Documents/Docker/data/neilico/show-admin-password.sh") else "✗")

    token = login(original, email) or ""
    raw = sh("curl", "-s", BASE + "/api/v1/stream-rules", "-H", "Authorization: Bearer " + token)
    try:
        rules = json.loads(raw)
    except Exception:
        rules = {}
    for rule in rules.get("items", []):
        print(f"  端口转发规则: {rule['name']} {rule['protocol']} {rule['listen_port']} "
              f"状态={rule['status']} 错误={rule.get('last_error', '')[:60]}")
    print("  目标直连（判断是环境还是规则）:",
          sh("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "6",
             "http://100.64.0.2:9100/metrics"), "(000=NAS 不可达)")
    for path in ("/register", "/account", "/streams"):
        print(f"  {path} → {sh('curl', '-s', '-o', '/dev/null', '-w', '%{http_code}', '--max-time', '5', BASE + path)}")


if __name__ == "__main__":
    main()
