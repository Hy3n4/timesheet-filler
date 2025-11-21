# Timesheet Filler Helm Chart

A Helm chart for deploying the Timesheet Filler web application with comprehensive email provider support including Resend, SendGrid, AWS SES, OCI Email, and MailJet.

## Overview

Timesheet Filler is a web application that processes Excel timesheet files and allows users to edit and generate timesheet reports. This Helm chart provides a production-ready deployment with:

- Multiple email provider support (Resend, SendGrid, AWS SES, OCI Email, MailJet)
- Prometheus metrics integration
- Horizontal Pod Autoscaling
- Comprehensive security configurations
- Production-ready health checks
- External secret management support

## Prerequisites

- Kubernetes 1.19+
- Helm 3.2.0+
- (Optional) Prometheus Operator for ServiceMonitor support
- (Optional) External Secrets Operator for external secret management
- (Optional) cert-manager for automatic TLS certificate management

## Quick Start

### 1. Add Helm Repository (if published)

```bash
helm repo add timesheet-filler https://your-username.github.io/timesheet-filler
helm repo update
```

### 2. Install from Local Chart

```bash
# Clone the repository
git clone https://github.com/your-username/timesheet-filler.git
cd timesheet-filler/helm/timesheet-filler

# Install with default values (email disabled)
helm install my-timesheet-filler .

# Install with Resend email support
helm install my-timesheet-filler . \
  --set email.enabled=true \
  --set email.provider=resend \
  --set email.resend.apiKey=re_your_api_key_here \
  --set email.fromEmail=noreply@yourdomain.com \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=timesheet.yourdomain.com
```

## Email Provider Configuration

### Resend (Recommended)

Resend offers a modern, developer-friendly email API with excellent deliverability.

```yaml
email:
  enabled: true
  provider: resend
  fromName: "Timesheet System"
  fromEmail: "noreply@yourdomain.com"  # Must use verified domain
  resend:
    apiKey: "re_your_api_key_here"
```

**Setup Steps:**
1. Sign up at [resend.com](https://resend.com)
2. Verify your domain in the Resend dashboard
3. Generate an API key
4. Update DNS records (SPF, DKIM, DMARC) as shown in Resend

### SendGrid

```yaml
email:
  enabled: true
  provider: sendgrid
  fromName: "Timesheet System"
  fromEmail: "noreply@yourdomain.com"
  sendgrid:
    apiKey: "SG.your_sendgrid_api_key"
```

### AWS SES

```yaml
email:
  enabled: true
  provider: ses
  fromName: "Timesheet System"
  fromEmail: "noreply@yourdomain.com"
  ses:
    region: "us-east-1"
    accessKeyId: "your_access_key"
    secretAccessKey: "your_secret_key"
```

### MailJet

```yaml
email:
  enabled: true
  provider: mailjet
  fromName: "Timesheet System"
  fromEmail: "noreply@yourdomain.com"
  mailjet:
    apiKey: "your_mailjet_api_key"
    secretKey: "your_mailjet_secret_key"
```

### OCI Email

```yaml
email:
  enabled: true
  provider: oci
  fromName: "Timesheet System"
  fromEmail: "noreply@yourdomain.com"
  oci:
    compartmentId: "ocid1.compartment.oc1..example"
    profileName: "DEFAULT"
    endpointSuffix: "oraclecloud.com"
```

## Installation Examples

### Basic Development Setup

```bash
helm install timesheet-filler . \
  --set email.enabled=false \
  --set ingress.enabled=false \
  --set service.type=NodePort
```

### Production with Resend

```bash
helm install timesheet-filler . \
  --values examples/resend-values.yaml \
  --set email.resend.apiKey=$RESEND_API_KEY \
  --set email.fromEmail=noreply@yourcompany.com \
  --set ingress.hosts[0].host=timesheet.yourcompany.com
```

### Production with External Secrets

```bash
# First create the external secret
kubectl apply -f - <<EOF
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: timesheet-filler-email-credentials
spec:
  secretStoreRef:
    name: vault-backend
    kind: SecretStore
  target:
    name: timesheet-filler-email-credentials
    creationPolicy: Owner
  data:
  - secretKey: resend-api-key
    remoteRef:
      key: timesheet-filler/resend
      property: api-key
EOF

# Deploy with external secret reference
helm install timesheet-filler . \
  --values examples/production-values.yaml \
  --set email.existingSecret=timesheet-filler-email-credentials
```

## Configuration

### Core Application Settings

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Container image repository | `ghcr.io/hy3n4/timesheet-filler` |
| `image.tag` | Container image tag | `latest` |
| `app.port` | Application port | `8080` |
| `app.maxUploadSize` | Maximum upload size in bytes | `16777216` (16MB) |
| `app.fileTokenExpiry` | File token expiry duration | `24h` |

### Email Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `email.enabled` | Enable email functionality | `false` |
| `email.provider` | Email provider (sendgrid,ses,oci,mailjet,resend) | `resend` |
| `email.fromName` | Sender display name | `Timesheet Filler` |
| `email.fromEmail` | Sender email address | `noreply@example.com` |
| `email.defaultRecipients` | List of default recipients | `[]` |
| `email.existingSecret` | Use existing secret for credentials | `""` |

### Resend Specific

| Parameter | Description | Default |
|-----------|-------------|---------|
| `email.resend.apiKey` | Resend API key | `""` |

### Ingress Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `ingress.enabled` | Enable ingress | `false` |
| `ingress.className` | Ingress class name | `""` |
| `ingress.hosts` | List of hosts | `[{host: timesheet-filler.local, paths: [{path: /, pathType: Prefix}]}]` |
| `ingress.tls` | TLS configuration | `[]` |

### Monitoring

| Parameter | Description | Default |
|-----------|-------------|---------|
| `prometheus.enabled` | Enable Prometheus metrics | `false` |
| `prometheus.serviceMonitor.enabled` | Create ServiceMonitor | `false` |
| `prometheus.serviceMonitor.interval` | Scrape interval | `30s` |

### Autoscaling

| Parameter | Description | Default |
|-----------|-------------|---------|
| `autoscaling.enabled` | Enable HPA | `false` |
| `autoscaling.minReplicas` | Minimum replicas | `1` |
| `autoscaling.maxReplicas` | Maximum replicas | `10` |
| `autoscaling.targetCPUUtilizationPercentage` | CPU target | `80` |

## Security Considerations

### Secret Management

**Development/Testing:**
```yaml
email:
  resend:
    apiKey: "re_your_key_here"  # Direct value (not recommended for production)
```

**Production (Recommended):**
```yaml
email:
  existingSecret: "timesheet-filler-email-credentials"  # Reference external secret
```

### Network Policies

The chart supports network policies for enhanced security:

```yaml
networkPolicy:
  enabled: true
  policyTypes:
    - Ingress
    - Egress
```

### Security Contexts

The chart includes security contexts to run containers with minimal privileges:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65534
  fsGroup: 65534

securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop:
    - ALL
  readOnlyRootFilesystem: false
```

## Monitoring and Observability

### Prometheus Integration

Enable Prometheus metrics collection:

```yaml
prometheus:
  enabled: true
  serviceMonitor:
    enabled: true
    labels:
      release: prometheus-operator
```

### Health Checks

The application provides health check endpoints:
- `/healthz` - Liveness probe
- `/readyz` - Readiness probe
- `/metrics` - Prometheus metrics

### Logging

Application logs are written to stdout/stderr and can be collected by your logging solution.

## Migration from Docker Compose

If migrating from Docker Compose, here's the equivalent Helm configuration:

**Docker Compose:**
```yaml
environment:
  - EMAIL_ENABLED=true
  - EMAIL_PROVIDER=resend
  - RESEND_API_KEY=re_your_key
  - EMAIL_FROM_EMAIL=noreply@domain.com
```

**Helm Values:**
```yaml
email:
  enabled: true
  provider: resend
  fromEmail: "noreply@domain.com"
  resend:
    apiKey: "re_your_key"
```

## Troubleshooting

### Common Issues

1. **Email not sending:**
   ```bash
   # Check pod logs
   kubectl logs -l app.kubernetes.io/name=timesheet-filler

   # Verify email configuration
   kubectl describe secret timesheet-filler-email-credentials
   ```

2. **Ingress not working:**
   ```bash
   # Check ingress status
   kubectl get ingress
   kubectl describe ingress timesheet-filler

   # Verify cert-manager certificates (if using)
   kubectl get certificates
   ```

3. **Pod startup issues:**
   ```bash
   # Check pod events
   kubectl describe pod -l app.kubernetes.io/name=timesheet-filler

   # Check resource constraints
   kubectl top pods -l app.kubernetes.io/name=timesheet-filler
   ```

### Debug Mode

Enable debug logging:

```yaml
env:
  - name: LOG_LEVEL
    value: "debug"
```

### Validation

Validate your configuration before deployment:

```bash
# Dry-run installation
helm install timesheet-filler . --dry-run --debug --values your-values.yaml

# Template validation
helm template timesheet-filler . --values your-values.yaml | kubectl apply --dry-run=client -f -
```

## Upgrading

### From v0.x to v1.x

The v1.x release includes breaking changes:

1. **Values structure changed:** Email configuration moved to `email.*` namespace
2. **New labels:** Uses standard Kubernetes labels
3. **Security contexts:** Now enforced by default

**Migration steps:**

1. Backup existing configuration:
   ```bash
   helm get values timesheet-filler > old-values.yaml
   ```

2. Update values file format according to new structure

3. Perform upgrade:
   ```bash
   helm upgrade timesheet-filler . --values new-values.yaml
   ```

### Rolling Updates

The chart supports zero-downtime rolling updates:

```bash
# Update image tag
helm upgrade timesheet-filler . --set image.tag=v1.1.0

# Update configuration
helm upgrade timesheet-filler . --values updated-values.yaml
```

## Development

### Local Development

For local chart development:

```bash
# Lint chart
helm lint .

# Test template rendering
helm template test-release . --values examples/resend-values.yaml

# Package chart
helm package .
```

### Testing

Run chart tests:

```bash
# Install with test hooks
helm install timesheet-filler . --wait

# Run tests
helm test timesheet-filler
```

## Support

- **Documentation:** [GitHub Repository](https://github.com/your-username/timesheet-filler)
- **Issues:** [GitHub Issues](https://github.com/your-username/timesheet-filler/issues)
- **Email Migration:** [Resend Migration Guide](../../examples/RESEND_MIGRATION.md)

## Contributing

1. Fork the repository
2. Make your changes
3. Test thoroughly
4. Submit a pull request

## License

This Helm chart is licensed under the MIT License. See the LICENSE file for details.
