import assert from 'node:assert/strict'
import test from 'node:test'

import { sandboxStatusPollIntervalMs, shouldPollSandboxStatuses } from '../src/lib/sandboxPolling.js'

test('polls local running and offline agents so status changes refresh', () => {
  assert.equal(shouldPollSandboxStatuses([{ status: 'running', is_local: true }]), true)
  assert.equal(shouldPollSandboxStatuses([{ status: 'offline', is_local: true }]), true)
  assert.equal(sandboxStatusPollIntervalMs([{ status: 'running', is_local: true }]), 10000)
})

test('keeps fast polling for transitional sandbox states', () => {
  assert.equal(shouldPollSandboxStatuses([{ status: 'pausing', is_local: false }]), true)
  assert.equal(sandboxStatusPollIntervalMs([{ status: 'pausing', is_local: false }]), 2000)
})

test('does not keep polling stable cloud or paused local sandboxes', () => {
  assert.equal(shouldPollSandboxStatuses([{ status: 'running', is_local: false }]), false)
  assert.equal(shouldPollSandboxStatuses([{ status: 'paused', is_local: true }]), false)
  assert.equal(sandboxStatusPollIntervalMs([{ status: 'paused', is_local: true }]), null)
})
