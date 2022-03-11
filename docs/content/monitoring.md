# Monitoring with Prometheus

## prerequisites

- [Prometheus](https://github.com/prometheus-community/helm-charts/tree/main/charts/prometheus) - collects metrics from the cloudflare-operator controllers and Kubernetes API
- [Grafana](https://github.com/grafana/helm-charts/tree/main/charts/grafana) dashboards - displays cloudflare-operator stats
- [kube-state-metrics](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-state-metrics) - generates metrics about the state of the Kubernetes objects

The easiest way to deploy all necessary applications is to use [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack).

## Install cloudflare-operator Grafana dashboard

Note that the cloudflare-operator expose the `/metrics` endpoint on port `8080`. When using Prometheus Operator you need a `PodMonitor` object to configure scraping for the controllers.

Apply the `config/manifests/prometheus/monitor.yaml` containing the `PodMonitor` and create a configmap with the cloudflare-operator dashboard:

```bash
# Create a podmonitor
kubectl apply -f https://raw.githubusercontent.com/containeroo/cloudflare-operator/master/config/manifests/prometheus/monitor.yaml

# Download Grafana dashboard
wget https://raw.githubusercontent.com/containeroo/cloudflare-operator/master/config/manifests/grafana/dashboards/overview.json -O /tmp/grafana-dashboard-cloudflare-operator.json

# Create the configmap
kubectl create configmap grafana-dashboard-cloudflare-operator --from-file=/tmp/grafana-dashboard-cloudflare-operator.json

# Add label so Grafana can fetch dashboard
kubectl label configmap grafana-dashboard-cloudflare-operator grafana_dashboard="1"
```

## Metrics

For each `cf.containeroo.ch` kind, the controllers expose a gauge metric to track the Ready condition status.

Ready status metrics:

```text
cloudflare_operator_account_status
cloudflare_operator_dns_record_status
cloudflare_operator_ip_status
cloudflare_operator_zone_status
```

Alertmanager example:

```yaml
groups:
  - alert: DNSRecordFailures
    annotations:
      summary: DNSRecord {{ $labels.name }} ({{ $labels.record_name }}) in namespace
        {{ $labels.exported_namespace }} failed
    expr: cloudflare_operator_dns_record_status > 0
    for: 1m
    labels:
      severity: critical
```
