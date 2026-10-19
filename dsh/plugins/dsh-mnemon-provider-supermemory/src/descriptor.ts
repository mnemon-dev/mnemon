import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "supermemory",
  "label": "Supermemory",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "remote",
  "workspaceBinding": "provider-global",
  "summary": "Semantic memory, persistent profiles, conversation ingest, and multi-container recall.",
  "summaryI18nKey": "overview.providerSummary.supermemory",
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
    "deletionMode": "soft"
  },
  "fields": [
    {
      "key": "endpoint",
      "label": "Endpoint",
      "scope": "service",
      "input": "url",
      "required": true,
      "defaultValue": "https://api.supermemory.ai",
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
      "key": "containerTag",
      "label": "Container tag",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh",
      "maxLength": 100,
      "pattern": "^[a-zA-Z0-9_:-]+$",
      "i18nKey": "overview.providerField.containerTag"
    },
    {
      "key": "searchMode",
      "label": "Search mode",
      "scope": "memory",
      "input": "select",
      "required": true,
      "defaultValue": "hybrid",
      "options": [
        {
          "value": "hybrid",
          "label": "Hybrid",
          "i18nKey": "overview.providerOption.hybrid"
        },
        {
          "value": "memories",
          "label": "Memories",
          "i18nKey": "overview.providerOption.memories"
        },
        {
          "value": "documents",
          "label": "Documents",
          "i18nKey": "overview.providerOption.documents"
        }
      ],
      "i18nKey": "overview.providerField.searchMode"
    }
  ]
}
