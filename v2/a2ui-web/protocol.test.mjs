import assert from 'node:assert/strict'
import test from 'node:test'

import {
  A2UI_BASIC_CATALOG,
  APPROVAL_NAME,
  SSEDecoder,
  appendUserMessage,
  buildRunRequest,
  cloneViewState,
  createViewState,
  isTerminalRunStatus,
  readFragmentConfiguration,
  reduceAGUIEvent,
  resolveJSONPointer,
} from './protocol.js'

const sessionID = '50000000-0000-4000-8000-000000000005'

test('buildRunRequest keeps browser authority out of the request', () => {
  const request = buildRunRequest({
    sessionID,
    clientRunID: 'web-run-1',
    messageID: 'user-1',
    prompt: 'inspect the workspace',
    cursor: 'opaque-cursor',
  })
  assert.deepEqual(request, {
    threadId: sessionID,
    runId: 'web-run-1',
    messages: [{ id: 'user-1', role: 'user', content: 'inspect the workspace' }],
    tools: [],
    context: [],
    forwardedProps: { agentserver: { eventCursor: 'opaque-cursor' } },
  })
  assert.equal('state' in request, false)
  assert.deepEqual(readFragmentConfiguration('#token=secret&workspace=w&session=s'), {
    token: 'secret', workspaceID: 'w', sessionID: 's',
  })
})

test('SSEDecoder handles fragmented CRLF, comments, and multiple frames', () => {
  const decoder = new SSEDecoder(4096)
  const events = [
    ...decoder.push(': heartbeat\r\nda'),
    ...decoder.push('ta: {"type":"RUN_STARTED","runId":"run-1"}\r\n\r\ndata: {"type":"RUN_FIN'),
    ...decoder.push('ISHED"}\n\n'),
    ...decoder.finish(),
  ]
  assert.deepEqual(events.map((event) => event.type), ['RUN_STARTED', 'RUN_FINISHED'])
  assert.throws(() => new SSEDecoder(8).push('123456789'), /exceeds/)
})

test('reducer assembles AG-UI lifecycles and A2UI display surfaces', () => {
  let state = appendUserMessage(createViewState(), 'user-1', 'hello')
  const events = [
    { type: 'RUN_STARTED', runId: 'run-1' },
    { type: 'CUSTOM', name: 'agentserver.event_cursor', value: { version: 1, runId: 'run-1', cursor: 'cursor-1', lastEventSequence: 1 } },
    { type: 'TEXT_MESSAGE_START', messageId: 'message-1', role: 'assistant' },
    { type: 'TEXT_MESSAGE_CONTENT', messageId: 'message-1', delta: 'done' },
    { type: 'TEXT_MESSAGE_END', messageId: 'message-1' },
    { type: 'TOOL_CALL_START', toolCallId: 'call-1', toolCallName: 'executor.shell' },
    { type: 'TOOL_CALL_ARGS', toolCallId: 'call-1', delta: '{"command":"pwd"}' },
    { type: 'TOOL_CALL_END', toolCallId: 'call-1' },
    { type: 'TOOL_CALL_RESULT', toolCallId: 'call-1', messageId: 'tool-1', content: '/workspace' },
    { type: 'CUSTOM', name: 'a2ui.operations', value: [
      { version: 'v0.9', createSurface: { surfaceId: 'command-event-1', catalogId: A2UI_BASIC_CATALOG } },
      { version: 'v0.9', updateComponents: { surfaceId: 'command-event-1', components: [
        { id: 'root', component: 'Card', child: 'content' },
        { id: 'content', component: 'Column', children: ['command'] },
        { id: 'command', component: 'Text', text: { path: '/command' } },
      ] } },
      { version: 'v0.9', updateDataModel: { surfaceId: 'command-event-1', value: { command: 'pwd' } } },
    ] },
    { type: 'RUN_FINISHED', runId: 'run-1' },
  ]
  for (const event of events) state = reduceAGUIEvent(state, event)
  assert.equal(state.status, 'completed')
  assert.equal(state.messages[1].text, 'done')
  assert.equal(state.tools[0].result, '/workspace')
  assert.deepEqual(state.surfaceOrder, ['command-event-1'])
  assert.equal(resolveJSONPointer(state.surfaces['command-event-1'].dataModel, '/command'), 'pwd')

  const checkpoint = cloneViewState(state)
  checkpoint.messages[0].text = 'changed'
  assert.equal(state.messages[0].text, 'hello')
})

test('reducer fails closed on lifecycle gaps and cross-run cursors', () => {
  assert.throws(
    () => reduceAGUIEvent(createViewState(), { type: 'TEXT_MESSAGE_CONTENT', messageId: 'missing', delta: 'x' }),
    /before start/,
  )
  const running = reduceAGUIEvent(createViewState(), { type: 'RUN_STARTED', runId: 'run-1' })
	assert.throws(
	  () => reduceAGUIEvent(running, { type: 'CUSTOM', name: 'agentserver.event_cursor', value: { version: 1, runId: 'run-2', cursor: 'cursor', lastEventSequence: 1 } }),
	  /invalid lifecycle-safe/,
	)
	assert.throws(
	  () => reduceAGUIEvent(running, { type: 'CUSTOM', name: 'a2ui.operations', value: [
	    { version: 'v0.9', createSurface: { surfaceId: '__proto__', catalogId: A2UI_BASIC_CATALOG } },
	  ] }),
	  /forbidden object key/,
	)
})

test('explicit cancellation remains distinct from a failed run', () => {
  let state = reduceAGUIEvent(createViewState(), { type: 'RUN_STARTED', runId: 'run-1' })
  state = reduceAGUIEvent(state, {
    type: 'CUSTOM', name: 'agentserver.run_status',
    value: { runId: 'run-1', status: 'cancelling', code: 'user_cancelled', message: 'cancellation requested' },
  })
  assert.equal(state.status, 'cancelling')
  state = reduceAGUIEvent(state, {
    type: 'RUN_ERROR', runId: 'run-1', code: 'user_cancelled', message: 'cancelled by user',
  })
  assert.equal(state.status, 'cancelled')
  assert.equal(state.error.code, 'user_cancelled')
  assert.equal(isTerminalRunStatus(state.status), true)
  assert.equal(isTerminalRunStatus('cancelling'), false)
})

test('approval reducer keeps command authority separate from A2UI display data', () => {
  const runID = '40000000-0000-4000-8000-000000000004'
  let state = reduceAGUIEvent(createViewState(), { type: 'RUN_STARTED', runId: runID })
  const pending = {
    approvalId: '80000000-0000-4000-8000-000000000008',
    executionId: '70000000-0000-4000-8000-000000000007',
    runId: runID,
    runAttemptId: '50000000-0000-4000-8000-000000000005',
    runAttemptGeneration: 1,
    nonce: '90000000-0000-4000-8000-000000000009',
    contextDigest: {
      domain: 'approval-context', canonicalizerVersion: 'rfc8785-v1',
      sha256: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    },
    toolName: 'shell', status: 'pending', decision: '', approverId: '',
    expiresAt: '2026-07-31T12:10:00Z', version: 1,
  }
  state = reduceAGUIEvent(state, { type: 'CUSTOM', name: APPROVAL_NAME, value: pending })
  assert.deepEqual(state.approvalOrder, [pending.approvalId])
  assert.equal(state.approvals[pending.approvalId].status, 'pending')

  const approved = {
    ...pending, status: 'approved', decision: 'approve',
    approverId: '10000000-0000-4000-8000-000000000010', version: 2,
  }
  state = reduceAGUIEvent(state, { type: 'CUSTOM', name: APPROVAL_NAME, value: approved })
  state = reduceAGUIEvent(state, { type: 'CUSTOM', name: APPROVAL_NAME, value: approved })
  assert.equal(state.approvals[pending.approvalId].status, 'approved')

  const consumed = { ...approved, status: 'consumed', version: 3 }
  state = reduceAGUIEvent(state, { type: 'CUSTOM', name: APPROVAL_NAME, value: consumed })
  assert.equal(state.approvals[pending.approvalId].status, 'consumed')
  assert.throws(
    () => reduceAGUIEvent(state, { type: 'CUSTOM', name: APPROVAL_NAME, value: { ...consumed, nonce: '10000000-0000-4000-8000-000000000011', version: 4 } }),
    /immutable nonce/,
  )
  assert.equal(Object.keys(state.surfaces).length, 0)
})
