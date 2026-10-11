import { descriptor as native } from 'dsh-mnemon-provider-mnemon-native'
import { descriptor as openviking } from 'dsh-mnemon-provider-openviking'
import { descriptor as holographic } from 'dsh-mnemon-provider-holographic'
import { descriptor as honcho } from 'dsh-mnemon-provider-honcho'
import { descriptor as mem0 } from 'dsh-mnemon-provider-mem0'
import { descriptor as hindsight } from 'dsh-mnemon-provider-hindsight'
import { descriptor as retaindb } from 'dsh-mnemon-provider-retaindb'
import { descriptor as byterover } from 'dsh-mnemon-provider-byterover'
import { descriptor as supermemory } from 'dsh-mnemon-provider-supermemory'

/** Explicit test assembly, never a production built-in Provider registry. */
export const TEST_PROVIDERS = [native, openviking, holographic, honcho, mem0, hindsight, retaindb, byterover, supermemory]
