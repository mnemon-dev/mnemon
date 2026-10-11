import type { MemoryProviderDescriptor } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

export const descriptor: MemoryProviderDescriptor = {
  "id": "mnemon-native",
  "label": "Mnemon Native",
  "icon": {
    "kind": "brand",
    "value": "mnemon"
  },
  "kind": "local",
  "workspaceBinding": "automatic",
  "summary": "Official local-first memory with exact writes, typed graph relations, and soft deletion.",
  "summaryI18nKey": "overview.providerSummary.mnemon-native",
  "origin": "native",
  "capabilities": {
    "search": true,
    "browse": true,
    "graph": true,
    "entities": true,
    "related": true,
    "remember": true,
    "link": true,
    "forget": true,
    "writeMode": "exact",
    "deletionMode": "soft"
  },
  "fields": []
}
