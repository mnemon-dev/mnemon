import { memo, useCallback, useEffect, useId, useRef, useState, useSyncExternalStore, type JSX } from 'react'
import { Button, Modal, Tooltip } from '@deepseek-ai/dsh-client-ui-primitives'
import type { ClientConnectionHandle, ClientSettingsScope, Config } from "../host/protocol.ts"
import { MnemonClient } from './api.ts'
import { dispatchMnemonAnchor } from './anchor.ts'
import type { MnemonKey } from './locales.ts'
import type { MnemonClientContext } from './dsh-context.ts'
import { MemoryIcon } from './memory-icon.tsx'
import { message } from './page-kit.tsx'
import { TaskAgentTag, WriteReceipt } from './page-controls.tsx'
import css from './MnemonSaveAction.module.css'

interface MnemonSaveActionProps {
  /** Stable identity of the finalized assistant message this action addresses. */
  messageId: string
  /** Injected by the slot host: the session this message belongs to. */
  sessionId?: string
  connection: ClientConnectionHandle
  settingsScope: ClientSettingsScope<Config>
  localeRuntime: Pick<MnemonClientContext['locale'], 'getSnapshot' | 'subscribe'>
  t: (key: MnemonKey, params?: Record<string, unknown>) => string
}

/** What the task Agent did with the text it was given. */
interface SaveOutcome {
  /** The candidate the outcome answers; sending it again waits for an edit. */
  content: string
  action?: string
  summary?: string
  error?: string
}

const PREVIEW_LIMIT = 8000

/**
 * Save-to-memory action on finalized assistant messages. A task Agent decides
 * whether the (editable) reply is worth keeping and writes it; the dialog
 * shows the same receipt as Save to memory on the Memory Spaces page.
 */
export const MnemonSaveAction = memo(function MnemonSaveAction({ messageId, sessionId, connection, settingsScope, localeRuntime, t }: MnemonSaveActionProps): JSX.Element {
  const subscribeLocale = useCallback((listener: () => void) => localeRuntime.subscribe(listener), [localeRuntime])
  const getLocale = useCallback(() => localeRuntime.getSnapshot(), [localeRuntime])
  useSyncExternalStore(subscribeLocale, getLocale, getLocale)
  const subscribeSettings = useCallback((listener: () => void) => settingsScope.subscribe(listener), [settingsScope])
  const getSettingsSnapshot = useCallback(() => settingsScope.getSnapshot(), [settingsScope])
  const settingsSnapshot = useSyncExternalStore(subscribeSettings, getSettingsSnapshot, getSettingsSnapshot)
  const managementWritable = settingsSnapshot.status === 'ready' && settingsSnapshot.writable
  const [open, setOpen] = useState(false)
  const [writeEnabled, setWriteEnabled] = useState<boolean | undefined>(undefined)
  const [taskAgent, setTaskAgent] = useState<boolean | undefined>(undefined)
  const [candidate, setCandidate] = useState<string | undefined>(undefined)
  const [truncated, setTruncated] = useState(false)
  const [missing, setMissing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [outcome, setOutcome] = useState<SaveOutcome | null>(null)
  const candidateId = useId()
  const openRef = useRef(false)
  const requestVersionRef = useRef(0)
  const submitActiveRef = useRef(false)

  const setPanelOpen = (next: boolean): void => {
    requestVersionRef.current += 1
    openRef.current = next
    setOpen(next)
  }

  useEffect(() => {
    if (!open) {
      setWriteEnabled(undefined)
      setTaskAgent(undefined)
      setCandidate(undefined)
      setTruncated(false)
      setMissing(false)
      setSubmitting(submitActiveRef.current)
      setOutcome(null)
      return
    }
    const requestVersion = ++requestVersionRef.current
    let alive = true
    setSubmitting(submitActiveRef.current)
    const client = new MnemonClient(connection, sessionId)
    client.status()
      .then(status => {
        if (!alive || requestVersionRef.current !== requestVersion) return
        setWriteEnabled(status.writeEnabled && managementWritable)
        setTaskAgent(status.lifecycle?.taskAgentAvailable)
      })
      .catch(() => { if (alive && requestVersionRef.current === requestVersion) setWriteEnabled(false) })
    client.assistantMessageText(messageId)
      .then(result => {
        if (!alive || requestVersionRef.current !== requestVersion) return
        if (result === null || result.text === '') setMissing(true)
        else {
          setTruncated(result.text.length > PREVIEW_LIMIT)
          setCandidate(result.text.slice(0, PREVIEW_LIMIT))
        }
      })
      .catch(() => { if (alive && requestVersionRef.current === requestVersion) setMissing(true) })
    return () => { alive = false }
  }, [open, connection, sessionId, messageId, managementWritable])

  const content = candidate?.trim() ?? ''
  // A result answers one text: sending the same text again waits for an edit.
  const answered = outcome !== null && outcome.content === content
  const canSubmit = content !== '' && !submitting && writeEnabled === true && taskAgent !== false && !answered

  const submit = (): void => {
    if (!canSubmit || submitActiveRef.current) return
    const requestVersion = requestVersionRef.current
    submitActiveRef.current = true
    setSubmitting(true)
    setOutcome(null)
    const client = new MnemonClient(connection, sessionId)
    client.supervise(content, messageId)
      .then(result => {
        if (openRef.current && requestVersionRef.current === requestVersion) setOutcome({ content, action: result.action, summary: result.summary })
      })
      .catch(reason => {
        if (openRef.current && requestVersionRef.current === requestVersion) setOutcome({ content, error: message(reason) })
      })
      .finally(() => {
        submitActiveRef.current = false
        if (openRef.current) setSubmitting(false)
      })
  }

  const viewMemory = (): void => {
    setPanelOpen(false)
    dispatchMnemonAnchor({ page: 'memory-spaces/content', ...(sessionId === undefined ? {} : { sessionId }) })
  }

  return (
    <div className={css.wrap}>
      <Tooltip label={t('saveAction.tooltip')} side="bottom" disabled={open}>
        <button
          type="button"
          className={css.button}
          aria-label={t('saveAction.button')}
          aria-haspopup="dialog"
          aria-expanded={open}
          onClick={() => setPanelOpen(!openRef.current)}
        >
          <span className={css.icon}><MemoryIcon size={16} /></span>
        </button>
      </Tooltip>
      <Modal
        open={open}
        onClose={() => setPanelOpen(false)}
        title={t('saveAction.title')}
        closeLabel={t('saveAction.close')}
        description={t('saveAction.hint')}
        className={css.modal as string}
        contentClassName={css.modalContent as string}
        footer={(
          <>
            <Button variant="outline" className={css.modalAction} disabled={submitting} onClick={() => setPanelOpen(false)}>
              {outcome === null ? t('common.cancel') : t('saveAction.close')}
            </Button>
            <Button variant="primary" className={css.modalAction} disabled={!canSubmit} onClick={submit}>
              {submitting ? t('saveAction.submitting') : t('saveAction.submit')}
            </Button>
          </>
        )}
      >
        {writeEnabled === false && <div className={css.readOnly} role="status">{t('saveAction.readOnly')}</div>}
        {candidate === undefined && !missing && <div className={css.status}>{t('saveAction.fetching')}</div>}
        {missing && <div className={css.status} role="status">{t('saveAction.missing')}</div>}
        {candidate !== undefined && (
          <div className={css.candidate}>
            <div className={css.candidateHeading}>
              <label htmlFor={candidateId}>{t('saveAction.candidate')}</label>
              {writeEnabled === true && taskAgent !== undefined && <TaskAgentTag available={taskAgent} t={t} />}
            </div>
            <textarea id={candidateId} rows={12} value={candidate} onChange={event => setCandidate(event.target.value)} autoFocus />
            {truncated && <small className={css.truncated}>{t('saveAction.truncated', { limit: PREVIEW_LIMIT })}</small>}
          </div>
        )}
        {outcome !== null && <WriteReceipt t={t} action={outcome.action} summary={outcome.summary} error={outcome.error} onView={viewMemory} />}
      </Modal>
    </div>
  )
})
