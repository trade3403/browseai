export type State = 'DEVELOPING' | 'WAIT_CI' | 'COMPLETED' | 'QUEUED' | 'PAUSED' | 'STOPPED'
export type Action = 'pause' | 'resume' | 'stop'
export interface Project { id: string; name: string; repository: string; progress: number; state: State }
export function transition(state: State, action: Action, previous: State = 'QUEUED'): State {
  if (state === 'STOPPED' || state === 'COMPLETED') return state
  if (action === 'stop') return 'STOPPED'
  if (action === 'pause' && state !== 'PAUSED') return 'PAUSED'
  if (action === 'resume' && state === 'PAUSED') return previous === 'PAUSED' || previous === 'STOPPED' || previous === 'COMPLETED' ? 'QUEUED' : previous
  return state
}
export const labels: Record<State,string> = {DEVELOPING:'开发中',WAIT_CI:'等待 CI',COMPLETED:'已完成',QUEUED:'排队中',PAUSED:'已暂停',STOPPED:'已停止'}
