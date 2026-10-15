#!/usr/bin/env bash
# Generates the self-signed serving certificate for the MachineDeployment
# validating webhook (env63 has no cert-manager), stores it as a TLS secret and
# prints the base64 caBundle to embed into the ValidatingWebhookConfiguration.
#
# Usage: hack/env63/gen-webhook-cert.sh
set -euo pipefail

NAMESPACE="${NAMESPACE:-launcher}"
SERVICE="${SERVICE:-cluster-management-webhook-service}"
SECRET="${SECRET:-cluster-management-webhook-server-cert}"
KUBECTL="${KUBECTL:-kubectl}"

OUT_DIR="$(mktemp -d)"
trap 'rm -rf "${OUT_DIR}"' EXIT

openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
    -keyout "${OUT_DIR}/tls.key" -out "${OUT_DIR}/tls.crt" \
    -subj "/CN=${SERVICE}.${NAMESPACE}.svc" \
    -addext "subjectAltName=DNS:${SERVICE},DNS:${SERVICE}.${NAMESPACE},DNS:${SERVICE}.${NAMESPACE}.svc,DNS:${SERVICE}.${NAMESPACE}.svc.cluster.local"

"${KUBECTL}" -n "${NAMESPACE}" create secret tls "${SECRET}" \
    --cert="${OUT_DIR}/tls.crt" --key="${OUT_DIR}/tls.key" \
    --dry-run=client -o yaml | "${KUBECTL}" apply -f -

echo "--- caBundle for the ValidatingWebhookConfiguration:"
base64 -w0 "${OUT_DIR}/tls.crt"
echo
