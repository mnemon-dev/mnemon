import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { ICON } from './icon.ts'

/** Browser-safe metadata owned by this Provider. */
export const descriptor: MemoryProviderDescriptor = {
  "id": "hindsight",
  "label": "Hindsight",
  "icon": {
    "kind": "data-url",
    "value": ICON
  },
  "kind": "remote",
  "workspaceBinding": "provider-global",
  "summary": "Knowledge-graph memory with entity resolution, observations, multi-strategy recall, and reflection.",
  "summaryI18nKey": "overview.providerSummary.hindsight",
  "origin": "third-party",
  "capabilities": {
    "search": true,
    "browse": true,
    "graph": true,
    "entities": true,
    "related": true,
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
      "defaultValue": "https://api.hindsight.vectorize.io",
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
      "key": "bankId",
      "label": "Memory bank",
      "scope": "memory",
      "input": "text",
      "required": true,
      "defaultValue": "dsh",
      "i18nKey": "overview.providerField.bankId"
    },
    {
      "key": "budget",
      "label": "Recall budget",
      "scope": "memory",
      "input": "select",
      "required": true,
      "defaultValue": "mid",
      "options": [
        {
          "value": "low",
          "label": "Low",
          "i18nKey": "overview.providerOption.low"
        },
        {
          "value": "mid",
          "label": "Medium",
          "i18nKey": "overview.providerOption.mid"
        },
        {
          "value": "high",
          "label": "High",
          "i18nKey": "overview.providerOption.high"
        }
      ],
      "i18nKey": "overview.providerField.budget"
    }
  ]
}
