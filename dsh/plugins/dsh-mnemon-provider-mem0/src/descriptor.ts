import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "mem0",
  "label": "Mem0",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "remote",
  "workspaceBinding": "provider-global",
  "summary": "Automatic fact extraction, semantic retrieval, reranking, and deduplication.",
  "summaryI18nKey": "overview.providerSummary.mem0",
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
    "writeMode": "async-extracting",
    "deletionMode": "hard"
  },
  "fields": [
    {
      "key": "endpoint",
      "label": "Endpoint",
      "scope": "service",
      "input": "url",
      "required": true,
      "defaultValue": "https://api.mem0.ai",
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
      "key": "mode",
      "label": "Mode",
      "scope": "service",
      "input": "select",
      "required": true,
      "defaultValue": "platform",
      "options": [
        {
          "value": "platform",
          "label": "Mem0 Platform",
          "i18nKey": "overview.providerOption.platform"
        },
        {
          "value": "self-hosted",
          "label": "Self-hosted server",
          "i18nKey": "overview.providerOption.self-hosted"
        }
      ],
      "i18nKey": "overview.providerField.mode"
    },
    {
      "key": "userId",
      "label": "User ID",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh-user",
      "i18nKey": "overview.providerField.userId"
    },
    {
      "key": "agentId",
      "label": "Agent ID",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh",
      "i18nKey": "overview.providerField.agentId"
    },
    {
      "key": "rerank",
      "label": "Rerank search results",
      "scope": "memory",
      "input": "boolean",
      "required": false,
      "defaultValue": false,
      "i18nKey": "overview.providerField.rerank"
    }
  ]
}
