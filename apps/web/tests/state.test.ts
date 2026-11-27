import {test} from 'node:test'
import assert from 'node:assert/strict'
import {transition} from '../src/state.ts'
test('pause preserves resumable waiting state',()=> {
  assert.equal(transition('WAIT_CI','pause'),'PAUSED')
  assert.equal(transition('PAUSED','resume','WAIT_CI'),'WAIT_CI')
})
test('terminal states never restart',()=> {
  for (const state of ['COMPLETED','STOPPED'] as const) {
    for (const action of ['pause','resume','stop'] as const) assert.equal(transition(state,action),state)
  }
})
test('stop is terminal and resume outside paused is a no-op',()=> {
  assert.equal(transition('PAUSED','stop'),'STOPPED')
  assert.equal(transition('WAIT_CI','resume'),'WAIT_CI')
})
