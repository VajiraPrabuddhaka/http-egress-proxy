# Egress Control Demo

This demonstrates OpenChoreo egress control using NetworkPolicies applied manually to the data plane namespace.

## Prerequisites

- OpenChoreo cluster running with `http-egress-proxy` deployed
- The proxy is running in namespace: `dp-default-default-development-f8e58905`

## Demo Flow

### Step 0: Verify the proxy works (no egress restriction)

```bash
# Get the proxy service endpoint
kubectl get svc -n dp-default-default-development-f8e58905

# Port-forward to access the proxy (or use the external endpoint)
kubectl port-forward -n dp-default-default-development-f8e58905 svc/http-egress-proxy-development 8081:8080 &

# Test: call an external API via the proxy
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://httpbin.org/get", "method": "GET"}' | jq .

# Expected: 200 response with httpbin data
```

### Step 1: Apply default-deny egress

```bash
kubectl apply -f 01-deny-all-egress.yaml
```

Now test again:
```bash
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://httpbin.org/get", "method": "GET"}' | jq .

# Expected: error - "egress request failed: dial tcp ... i/o timeout"
```

**All outbound traffic is now blocked.** The proxy cannot reach any external service.

### Step 2: Allow DNS resolution

```bash
kubectl apply -f 02-allow-dns.yaml
```

Test again:
```bash
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://httpbin.org/get", "method": "GET"}' | jq .

# Expected: still fails, but now the error shows DNS resolved (connection refused/timeout on port 443)
# The DNS resolves but TCP connection to external IP is still blocked
```

### Step 3: Allow intra-namespace traffic

```bash
kubectl apply -f 03-allow-intra-namespace.yaml
```

This ensures pod-to-pod communication within the namespace works. You can verify:
```bash
# From any pod in the namespace, curl the greeter service
kubectl exec -n dp-default-default-development-f8e58905 \
  $(kubectl get pod -n dp-default-default-development-f8e58905 -l openchoreo.dev/component=http-egress-proxy -o name) \
  -- wget -qO- http://greeter-service-development:9090/greeter/greet?name=test

# Expected: "Hello, test!" (internal traffic works)
```

### Step 4: Platform allowlist (allow specific external destinations)

```bash
kubectl apply -f 04-allow-specific-external.yaml
```

Now test external access:
```bash
# This should NOW work (HTTPS to external IPs is allowed)
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://httpbin.org/get", "method": "GET"}' | jq .

# Expected: 200 response from httpbin

# Test another allowed destination
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://jsonplaceholder.typicode.com/todos/1", "method": "GET"}' | jq .

# Expected: 200 response with todo data
```

### Step 4b (Alternative): FQDN-based rules with Cilium

If your cluster has Cilium CNI:
```bash
# Remove the CIDR-based rule
kubectl delete -f 04-allow-specific-external.yaml

# Apply FQDN-based rule instead
kubectl apply -f 04b-cilium-fqdn-egress.yaml
```

Now ONLY the specified FQDNs are reachable:
```bash
# This works (httpbin.org is in the allowlist)
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://httpbin.org/get", "method": "GET"}' | jq .

# This FAILS (example.com is NOT in the allowlist)
curl -s -X POST http://localhost:8081/proxy \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com", "method": "GET"}' | jq .
```

### Step 5: Component-specific approved egress

```bash
kubectl apply -f 05-component-specific-egress.yaml
```

This adds port 8443 access ONLY for the `http-egress-proxy` component.
Other components (like `greeter-service`) still cannot reach port 8443.

## Cleanup

```bash
kubectl delete -f ./ -n dp-default-default-development-f8e58905
```

## Key Takeaways

1. **Default deny** blocks all outbound - workloads are isolated by default
2. **DNS is essential** - must be explicitly allowed in deny-all scenarios
3. **K8s NetworkPolicy** supports CIDR-based egress (IP ranges + ports)
4. **CiliumNetworkPolicy** adds FQDN support for domain-based allowlisting
5. **Additive model** - each NetworkPolicy adds more allowed paths (they don't conflict)
6. **Per-component rules** can grant additional access to specific workloads
