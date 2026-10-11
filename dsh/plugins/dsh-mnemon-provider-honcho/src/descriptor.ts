import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "honcho",
  "label": "Honcho",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "remote",
  "workspaceBinding": "provider-global",
  "summary": "Cross-session user modelling, peer profiles, dialectic reasoning, and persistent conclusions.",
  "summaryI18nKey": "overview.providerSummary.honcho",
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
      "defaultValue": "https://api.honcho.dev",
      "i18nKey": "overview.providerEndpoint"
    },
    {
      "key": "apiKey",
      "label": "API key",
      "scope": "service",
      "input": "secret",
      "required": false,
      "i18nKey": "overview.providerApiKey"
    },
    {
      "key": "workspace",
      "label": "Workspace",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh",
      "i18nKey": "overview.providerField.workspace"
    },
    {
      "key": "userId",
      "label": "User peer",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh-user",
      "i18nKey": "overview.providerField.userId"
    },
    {
      "key": "agentId",
      "label": "Agent peer",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh",
      "i18nKey": "overview.providerField.agentId"
    }
  ]
}
