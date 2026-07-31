{{- define "gvList" -}}
{{- $groupVersions := . -}}
---
title: "API Reference"
description: "cloudflare-operator API Reference"
weight: 50
---

## Packages
{{- range $groupVersions }}
- {{ markdownRenderGVLink . }}
{{- end }}

{{ range $groupVersions }}
{{ template "gvDetails" . }}
{{ end }}

_This page is generated from the operator API types with [crd-ref-docs](https://github.com/elastic/crd-ref-docs)._
{{- end -}}
