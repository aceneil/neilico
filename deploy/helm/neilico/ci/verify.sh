#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

DEFAULT_RENDER="${DEFAULT_RENDER:-/tmp/neilico-render-default.yaml}"
FULL_RENDER="${FULL_RENDER:-/tmp/neilico-render-full.yaml}"
BAD_VALUES="$(mktemp)"
BAD_RENDER="$(mktemp)"
trap 'rm -f "$BAD_VALUES" "$BAD_RENDER"' EXIT

helm template neilico --values neilico/ci/default-values.yaml >"$DEFAULT_RENDER"
helm template neilico --values neilico/ci/default-values.yaml \
  --set ingress.enabled=true \
  --set postgres.enabled=true \
  --set redis.enabled=true \
  --set nats.enabled=true >"$FULL_RENDER"

python3 - "$DEFAULT_RENDER" "$FULL_RENDER" <<'PY'
import pathlib
import re
import sys

import yaml


def load(path):
    raw = pathlib.Path(path).read_text()
    assert raw.strip(), f"{path} is empty"
    docs = list(yaml.safe_load_all(raw))
    assert docs and all(doc is not None for doc in docs), f"{path} has empty YAML documents"
    return docs


default_docs = load(sys.argv[1])
full_docs = load(sys.argv[2])


def find(docs, kind, name_suffix=None, component=None):
    matches = []
    for doc in docs:
        if not isinstance(doc, dict) or doc.get("kind") != kind:
            continue
        metadata = doc.get("metadata", {})
        name = metadata.get("name", "")
        labels = metadata.get("labels", {})
        if name_suffix and not name.endswith(name_suffix):
            continue
        if component and labels.get("app.kubernetes.io/component") != component:
            continue
        matches.append(doc)
    return matches


for docs, label in ((default_docs, "default"), (full_docs, "full")):
    assert find(docs, "Deployment", "control-api", "control-api"), f"{label}: control-api Deployment missing"
    assert find(docs, "Deployment", "dashboard", "dashboard"), f"{label}: dashboard Deployment missing"
    assert find(docs, "Service"), f"{label}: Service missing"

assert not find(default_docs, "Ingress"), "default render unexpectedly contains Ingress"
assert find(full_docs, "Ingress"), "full render is missing Ingress"
assert not find(default_docs, "StatefulSet"), "default render unexpectedly contains StatefulSet"
for component in ("postgres", "redis", "nats"):
    assert find(full_docs, "StatefulSet", component, component), f"full render is missing {component} StatefulSet"

allowed_secret_values = {"", "CHANGE_ME"}
for path in (sys.argv[1], sys.argv[2]):
    raw = pathlib.Path(path).read_text()
    assert not re.search(r"POSTGRES_PASSWORD=(?!CHANGE_ME(?:\s|$))[^\s]+", raw), (
        f"{path}: inline non-placeholder POSTGRES_PASSWORD detected"
    )
    for doc in yaml.safe_load_all(raw):
        if not isinstance(doc, dict) or doc.get("kind") != "Secret":
            continue
        for section in ("data", "stringData"):
            for key, value in (doc.get(section) or {}).items():
                assert value in allowed_secret_values, (
                    f"{path}: Secret key {key} contains a non-placeholder value"
                )

for doc in default_docs + full_docs:
    if not isinstance(doc, dict) or "metadata" not in doc:
        continue
    labels = doc["metadata"].get("labels", {})
    for required in (
        "app.kubernetes.io/name",
        "app.kubernetes.io/instance",
        "app.kubernetes.io/version",
        "app.kubernetes.io/managed-by",
        "app.kubernetes.io/component",
    ):
        assert required in labels, f"{doc.get('kind')} {doc['metadata'].get('name')} lacks {required}"
    assert doc["metadata"].get("annotations", {}).get("helm.sh/chart"), (
        f"{doc.get('kind')} {doc['metadata'].get('name')} lacks helm.sh/chart annotation"
    )
    if doc.get("kind") in {"Deployment", "StatefulSet", "DaemonSet"}:
        pod_annotations = doc.get("spec", {}).get("template", {}).get("metadata", {}).get("annotations", {})
        assert pod_annotations.get("checksum/config"), (
            f"{doc['metadata'].get('name')} lacks ConfigMap checksum rollout annotation"
        )
        assert pod_annotations.get("checksum/secret"), (
            f"{doc['metadata'].get('name')} lacks Secret checksum rollout annotation"
        )

print(f"default YAML parsed: {len(default_docs)} documents")
print(f"full YAML parsed: {len(full_docs)} documents")
print("required kinds, checksums, and secret placeholders: OK")
PY

cat >"$BAD_VALUES" <<'YAML'
replicaCount: "abc"
YAML
set +e
helm template neilico --values neilico/ci/default-values.yaml --values "$BAD_VALUES" >"$BAD_RENDER" 2>&1
bad_rc=$?
set -e
if [ "$bad_rc" -eq 0 ]; then
  echo "schema validation unexpectedly accepted replicaCount: \"abc\"" >&2
  exit 1
fi
echo "invalid replicaCount rejected with exit code $bad_rc"

echo "verify.sh: OK"
