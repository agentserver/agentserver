import assert from 'node:assert/strict'
import test from 'node:test'

import {
  executorEnrollmentPath,
  validateExecutorList,
  validateMemberList,
  validateWorkspaceList,
  workspaceMemberPath,
} from './resources.js'

const workspaceID = '71000000-0000-4000-8000-000000000011'
const userID = '72000000-0000-4000-8000-000000000012'
const executorID = '73000000-0000-4000-8000-000000000013'
const timestamp = '2026-08-04T00:00:00Z'

test('validates bounded Platform resource projections and exact paths', () => {
  const workspaces = validateWorkspaceList({ workspaces: [{
    workspaceId: workspaceID, name: 'SG Workspace', status: 'active', currentUserRole: 'owner', version: 1,
    createdAt: timestamp, updatedAt: timestamp,
  }] })
  const members = validateMemberList({ members: [{ userId: userID, role: 'developer', version: 2, createdAt: timestamp, updatedAt: timestamp }] })
  const executors = validateExecutorList({ executors: [{
    executorId: executorID, workspaceId: workspaceID, status: 'enrolling', version: 1, createdAt: timestamp, updatedAt: timestamp,
  }] }, workspaceID)
  assert.equal(workspaces[0].name, 'SG Workspace')
  assert.equal(members[0].role, 'developer')
  assert.equal(executors[0].executorId, executorID)
  assert.equal(workspaceMemberPath(workspaceID, userID), `/v2/workspaces/${workspaceID}/members/${userID}`)
  assert.equal(executorEnrollmentPath(workspaceID, executorID), `/v2/workspaces/${workspaceID}/executors/${executorID}:enrollmentToken`)
})

test('rejects resource projections that escape their requested workspace', () => {
  assert.throws(() => validateExecutorList({ executors: [{
    executorId: executorID, workspaceId: userID, status: 'online', version: 1, createdAt: timestamp, updatedAt: timestamp,
  }] }, workspaceID))
  assert.throws(() => validateWorkspaceList({ workspaces: [], extra: true }))
})
