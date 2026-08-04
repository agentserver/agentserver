import assert from 'node:assert/strict'
import test from 'node:test'

import {
  archiveUserSessionPath,
  sessionTitleFromPrompt,
  userSessionPath,
  userSessionsPath,
  validateUserSessionList,
  validateUserSessionMutation,
} from './sessions.js'

const workspaceID = '40000000-0000-4000-8000-000000000004'
const sessionID = '50000000-0000-4000-8000-000000000005'
const session = {
  sessionId: sessionID,
  workspaceId: workspaceID,
  title: 'Inspect deployment',
  status: 'active',
  version: 2,
  createdAt: '2026-08-04T00:00:00Z',
  updatedAt: '2026-08-04T00:01:00Z',
}

test('session routes remain inside one canonical workspace', () => {
  assert.equal(userSessionsPath(workspaceID), `/v2/workspaces/${workspaceID}/sessions`)
  assert.equal(userSessionPath(workspaceID, sessionID), `/v2/workspaces/${workspaceID}/sessions/${sessionID}`)
  assert.equal(archiveUserSessionPath(workspaceID, sessionID), `/v2/workspaces/${workspaceID}/sessions/${sessionID}/actions/archive`)
  assert.throws(() => userSessionsPath('../other'), /canonical UUID/)
})

test('session list and mutation validation reject scope escape and unknown fields', () => {
  assert.deepEqual(validateUserSessionList({ sessions: [session] }, workspaceID), [session])
  assert.equal(validateUserSessionMutation({ session, created: true }, workspaceID, sessionID, 'created').created, true)
  assert.throws(() => validateUserSessionList({ sessions: [{ ...session, workspaceId: '60000000-0000-4000-8000-000000000006' }] }, workspaceID), /scope/)
  assert.throws(() => validateUserSessionList({ sessions: [{ ...session, future: true }] }, workspaceID), /unknown/)
})

test('first prompt produces a bounded useful session title', () => {
  assert.equal(sessionTitleFromPrompt('  inspect\n\nthis deployment  '), 'inspect this deployment')
  assert.ok(new TextEncoder().encode(sessionTitleFromPrompt('界'.repeat(100))).length <= 120)
})
