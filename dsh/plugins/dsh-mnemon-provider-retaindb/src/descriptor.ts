import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "retaindb",
  "label": "RetainDB",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "remote",
  "workspaceBinding": "provider-global",
  "summary": "Cloud memory with hybrid vector/BM25 retrieval, profiles, and typed durable facts.",
  "summaryI18nKey": "overview.providerSummary.retaindb",
  "origin": "third-party",
  "capabilities": {
    "search": true,
    "browse": true,
    "graph": false,
    "entities": false,
    "related": false,
    "remember": true,
    "link": false,
    "forget": true,
    "writeMode": "exact",
    "deletionMode": "hard"
  },
  "fields": [
    {
      "key": "endpoint",
      "label": "Endpoint",
      "scope": "service",
      "input": "url",
      "required": true,
      "defaultValue": "https://api.retaindb.com",
      "i18nKey": "overview.providerEndpoint"
    },
    {
      "key": "apiKey",
      "label": "API key",
      "scope": "service",
      "input": "secret",
      "required": true,
      "i18nKey": "overview.providerApiKey"
    },
    {
      "key": "project",
      "label": "Project",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh",
      "i18nKey": "overview.providerField.project"
    },
    {
      "key": "userId",
      "label": "User ID",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh-user",
      "i18nKey": "overview.providerField.userId"
    }
  ]
}
