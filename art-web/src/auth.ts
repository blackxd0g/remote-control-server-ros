import { reactive } from 'vue'
import { api } from './api'
import type { CurrentUser } from './types'

interface AuthState {
  user: CurrentUser | null
  ready: boolean
}

const state = reactive<AuthState>({ user: null, ready: false })
function acceptLogin(result: {access_token:string;user:CurrentUser}) { sessionStorage.setItem(api.tokenKey,result.access_token);state.user=result.user }

export const auth = {
	state,
	can(permission:string) { return state.user?.role === 'admin' || state.user?.permissions?.includes('*') || state.user?.permissions?.includes(permission) || false },
  async restore() {
    if (!sessionStorage.getItem(api.tokenKey)) {
      state.ready = true
      return
    }
    try {
      state.user = await api.me()
    } catch {
      sessionStorage.removeItem(api.tokenKey)
    } finally {
      state.ready = true
    }
  },
  async login(username: string, password: string, verificationCode = '') {
    const result = await api.login(username, password, verificationCode)
    acceptLogin(result)
	},
	accept(result: {access_token:string;user:CurrentUser}) {
		acceptLogin(result)
  },
  async logout() {
    try {
      await api.logout()
    } finally {
      sessionStorage.removeItem(api.tokenKey)
      state.user = null
    }
  },
}
