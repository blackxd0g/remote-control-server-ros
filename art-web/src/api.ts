import type { ACLEvaluation, ACLRule, AutomationRule, AutomationRun, AddressBook, AddressBookEntry, AddressBookGrant, APIToken, AuditEvent, AuditPage, AuditQuery, AuditSummary, BackupArtifact, BackupInspection, BuilderWorker, ClientBuild, ClientProfile, ClientProfileAssignment, ClientProfileBundle, ClusterState, ConnectionContainment, CurrentUser, DashboardLayout, Device, Diagnostics, Infrastructure, LiveConnectionSnapshot, LoginResponse, ManagedGroup, ManagedUser, Notification, OIDCIdentity, PresenceSnapshot, RelayMetric, RelayServer, RoleDefinition, RuntimeSettings, ServiceCommand, Session, SessionPage, SessionSummary, Strategy, StrategyEvaluation, StrategySettingDefinition, TOTPEnrollment, Webhook, WebhookDelivery } from './types'

const tokenKey = 'rds.access_token'
const legacyTokenKey = 'art.access_token'

function storedToken(): string | null {
  const token = sessionStorage.getItem(tokenKey)
  if (token) return token
  const legacy = sessionStorage.getItem(legacyTokenKey)
  if (legacy) {
    sessionStorage.setItem(tokenKey, legacy)
    sessionStorage.removeItem(legacyTokenKey)
  }
  return legacy
}

function webDeviceID(): string {
  const bytes = new Uint8Array(16)
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256)
    }
  }
  return `web-${Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')}`
}

export class APIError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly payload: Record<string, unknown> = {},
  ) {
    super(message)
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')
  const token = storedToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    const payload = await response.json().catch(() => ({ error: response.statusText })) as { error?: string } & Record<string, unknown>
    if (response.status === 401) {
      sessionStorage.removeItem(tokenKey)
      sessionStorage.removeItem(legacyTokenKey)
    }
    throw new APIError(payload.error || 'Request failed', response.status, payload)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

async function download(path:string):Promise<{blob:Blob;filename:string}> {
  const headers = new Headers({Accept:'application/octet-stream'})
  const token = storedToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const response = await fetch(path,{headers})
  if (!response.ok) {
    const payload=await response.json().catch(()=>({error:response.statusText})) as {error?:string}
    throw new APIError(payload.error||'Download failed',response.status,payload)
  }
  const disposition=response.headers.get('Content-Disposition')||''
  const filename=/filename="?([^";]+)"?/i.exec(disposition)?.[1]||'rustdesk-server-routeros-backup.db'
  return {blob:await response.blob(),filename}
}

async function uploadImage(path:string,file:File,field:string):Promise<{avatar:string}> {
  const body=new FormData();body.append(field,file)
  const headers=new Headers({Accept:'application/json'});const token=storedToken();if(token)headers.set('Authorization',`Bearer ${token}`)
  const response=await fetch(path,{method:'POST',headers,body})
  const payload=await response.json().catch(()=>({error:response.statusText})) as {avatar?:string;error?:string}
  if(!response.ok)throw new APIError(payload.error||'Image upload failed',response.status,payload)
  return payload as {avatar:string}
}

export const api = {
  tokenKey,
  login: (username: string, password: string, verificationCode = '') => request<LoginResponse>('/api/login', {
    method: 'POST',
    body: JSON.stringify({
      username,
      password,
      verification_code: verificationCode,
      type: 'web',
      autoLogin: false,
      uuid: webDeviceID(),
      deviceInfo: { os: navigator.platform, type: 'web', name: navigator.userAgent },
    }),
  }),
  loginOptions: () => request<{name:string}[]>('/api/login-options'),
  registrationOptions: () => request<{enabled:boolean;approval_required:boolean}>('/api/registration-options'),
  register: (input:{username:string;email:string;display_name:string;password:string}) => request<{status:'pending'|'approved';message:string}>('/api/register',{method:'POST',body:JSON.stringify(input)}),
  beginOIDC: async (provider:string) => { const id='web-console',uuid=webDeviceID();const result=await request<{code:string;url:string}>('/api/oidc/auth',{method:'POST',body:JSON.stringify({op:provider,id,uuid,deviceInfo:{os:navigator.platform,type:'web',name:navigator.userAgent}})});return {...result,id,uuid} },
  pollOIDC: (code:string,id:string,uuid:string) => request<LoginResponse|{error:string}>(`/api/oidc/auth-query?code=${encodeURIComponent(code)}&id=${encodeURIComponent(id)}&uuid=${encodeURIComponent(uuid)}`),
  oidcIdentities: () => request<OIDCIdentity[]>('/api/oidc/identities'),
  beginOIDCLink: () => request<{code:string;url:string}>('/api/oidc/link', {method:'POST'}),
  pollOIDCLink: (code:string) => request<OIDCIdentity|{pending:true}>(`/api/oidc/link-query?code=${encodeURIComponent(code)}`),
  unlinkOIDC: (value:OIDCIdentity) => request<null>(`/api/oidc/identity?provider=${encodeURIComponent(value.provider)}&subject=${encodeURIComponent(value.subject)}`, {method:'DELETE'}),
  adminOIDCIdentities: () => request<OIDCIdentity[]>('/api/admin/oidc/identities'),
  adminUnlinkOIDC: (value:OIDCIdentity) => request<null>(`/api/admin/oidc/identity?provider=${encodeURIComponent(value.provider)}&subject=${encodeURIComponent(value.subject)}&user_id=${encodeURIComponent(value.user_id)}`, {method:'DELETE'}),
  me: () => request<CurrentUser>('/api/me'),
	apiTokens: () => request<APIToken[]>('/api/api-tokens'),
	createAPIToken: (name:string,ttlDays:number) => request<{token:string;details:APIToken}>('/api/api-tokens',{method:'POST',body:JSON.stringify({name,ttl_days:ttlDays})}),
	revokeAPIToken: (id:string) => request<null>(`/api/api-tokens/${id}`,{method:'DELETE'}),
  logout: () => request<null>('/api/logout', { method: 'POST' }),
  enrollTOTP: () => request<TOTPEnrollment>('/api/mfa/totp/enroll', { method: 'POST' }),
  confirmTOTP: (code:string) => request<CurrentUser>('/api/mfa/totp/confirm', { method: 'POST', body: JSON.stringify({code}) }),
  disableTOTP: (code:string) => request<null>('/api/mfa/totp', { method: 'DELETE', body: JSON.stringify({code}) }),
  regenerateRecoveryCodes: (code:string) => request<{recovery_codes:string[]}>('/api/mfa/totp/recovery-codes', { method: 'POST', body: JSON.stringify({code}) }),
  resetUserTOTP: (id:string) => request<null>(`/api/admin/users/${id}/mfa`, { method: 'DELETE' }),
  users: () => request<ManagedUser[]>('/api/admin/users'),
  createUser: (input: Record<string, unknown>) => request<ManagedUser>('/api/admin/users', {
    method: 'POST', body: JSON.stringify(input),
  }),
  updateUser: (id:string,input:Record<string,unknown>) => request<ManagedUser>(`/api/admin/users/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
  deleteUser: (id:string) => request<null>(`/api/admin/users/${id}`,{method:'DELETE'}),
  setEnabled: (id: string, enabled: boolean) => request<ManagedUser>(`/api/admin/users/${id}/${enabled ? 'enable' : 'disable'}`, { method: 'POST' }),
  forceRelogin: (id: string) => request<ManagedUser>(`/api/admin/users/${id}/force-relogin`, { method: 'POST' }),
  approveUser: (id:string) => request<ManagedUser>(`/api/admin/users/${id}/approve`,{method:'POST'}),
  rejectUser: (id:string) => request<ManagedUser>(`/api/admin/users/${id}/reject`,{method:'POST'}),
  setUserPassword: (id: string, password: string) => request<null>(`/api/admin/users/${id}/password`, { method: 'POST', body: JSON.stringify({ password }) }),
  devices: (archived=false) => request<Device[]>(`/api/admin/devices${archived?'?archived=true':''}`),
  updateDevice: (id:string,input:{alias:string;group_id:string;tags:string[]}) => request<Device>(`/api/admin/devices/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
  bulkUpdateDevices: (input:{ids:string[];group_id?:string;add_tags:string[];remove_tags:string[]}) => request<Device[]>('/api/admin/devices',{method:'PATCH',body:JSON.stringify(input)}),
  archiveDevice: (id:string) => request<Device>(`/api/admin/devices/${encodeURIComponent(id)}/archive`,{method:'POST'}),
  restoreDevice: (id:string) => request<Device>(`/api/admin/devices/${encodeURIComponent(id)}/restore`,{method:'POST'}),
  deleteArchivedDevice: (id:string) => request<null>(`/api/admin/devices/${encodeURIComponent(id)}`,{method:'DELETE'}),
  exportDevices: () => download('/api/admin/devices-export.csv'),
  importDevices: async (file:File) => {
    const headers=new Headers({Accept:'application/json','Content-Type':'text/csv'})
    const token=storedToken();if(token)headers.set('Authorization',`Bearer ${token}`)
    const response=await fetch('/api/admin/devices-import.csv',{method:'POST',headers,body:file})
    const payload=await response.json().catch(()=>({error:response.statusText})) as {imported?:number;error?:string}
    if(!response.ok)throw new APIError(payload.error||'Device import failed',response.status,payload as Record<string,unknown>)
    return payload as {imported:number}
  },
  groups: () => request<ManagedGroup[]>('/api/admin/groups'),
  createGroup: (input: { name: string; description: string; kind: 'user' | 'device' }) => request<ManagedGroup>('/api/admin/groups', { method: 'POST', body: JSON.stringify(input) }),
  updateGroup: (id:string,input:{name:string;description:string}) => request<ManagedGroup>(`/api/admin/groups/${id}`, { method:'PATCH', body:JSON.stringify(input) }),
  deleteGroup: (id:string) => request<null>(`/api/admin/groups/${id}`, { method:'DELETE' }),
  groupMembers: (id: string) => request<ManagedUser[]>(`/api/admin/groups/${id}/members`),
  addGroupMember: (groupID: string, userID: string) => request<unknown>(`/api/admin/groups/${groupID}/members/${userID}`, { method: 'PUT' }),
  removeGroupMember: (groupID: string, userID: string) => request<unknown>(`/api/admin/groups/${groupID}/members/${userID}`, { method: 'DELETE' }),
  sessions: () => request<Session[]>('/api/admin/sessions'),
  querySessions: (input:{limit:number;offset:number;status?:string;search?:string;user_id?:string}) => request<SessionPage>(`/api/admin/sessions/query?${new URLSearchParams(Object.entries(input).filter(([,value])=>value!==undefined&&value!=='').map(([key,value])=>[key,String(value)])).toString()}`),
  sessionSummary: () => request<SessionSummary>('/api/admin/sessions/summary'),
  bulkRevokeSessions: (ids:string[]) => request<{revoked:number}>('/api/admin/sessions/revoke',{method:'POST',body:JSON.stringify({ids})}),
  revokeSession: (id: string) => request<null>(`/api/sessions/${id}/revoke`, { method: 'POST' }),
  audit: () => request<AuditEvent[]>('/api/admin/audit?limit=200'),
  queryAudit: (input:AuditQuery) => request<AuditPage>(`/api/admin/audit/query?${new URLSearchParams(Object.entries(input).filter(([,value])=>value!==undefined&&value!=='').map(([key,value])=>[key,String(value)])).toString()}`),
  auditSummary: (input:AuditQuery) => request<AuditSummary>(`/api/admin/audit/summary?${new URLSearchParams(Object.entries(input).filter(([key,value])=>key!=='limit'&&key!=='offset'&&value!==undefined&&value!=='').map(([key,value])=>[key,String(value)])).toString()}`),
  exportAudit: (input:AuditQuery) => download(`/api/admin/audit/export.csv?${new URLSearchParams(Object.entries(input).filter(([key,value])=>key!=='limit'&&key!=='offset'&&value!==undefined&&value!=='').map(([key,value])=>[key,String(value)])).toString()}`),
  notifications: () => request<{notifications:Notification[];unread:number}>('/api/admin/notifications?limit=50&unread=true'),
  markNotificationRead: (id:string) => request<null>(`/api/admin/notifications/${id}/read`,{method:'POST'}),
  markAllNotificationsRead: () => request<null>('/api/admin/notifications/read-all',{method:'POST'}),
  settings: () => request<RuntimeSettings>('/api/admin/settings'),
	updateSettings: (body:Pick<RuntimeSettings,'require_login'|'require_device_deployment'|'registration_enabled'|'registration_auto_approve'|'access_token_ttl'|'session_ttl'|'mfa_mode'|'password_minimum_length'|'password_require_upper'|'password_require_lower'|'password_require_number'|'password_require_special'>) => request<RuntimeSettings>('/api/admin/settings',{method:'PATCH',body:JSON.stringify(body)}),
  uploadLogo: async (file:File) => {
    const body=new FormData();body.append('logo',file)
    const headers=new Headers({Accept:'application/json'});const token=storedToken();if(token)headers.set('Authorization',`Bearer ${token}`)
    const response=await fetch('/api/admin/settings/logo',{method:'POST',headers,body})
    if(!response.ok){const payload=await response.json().catch(()=>({error:response.statusText})) as {error?:string};throw new APIError(payload.error||'Logo upload failed',response.status,payload)}
    return response.json() as Promise<{custom_logo:boolean;logo_url:string;media_type:string}>
  },
  resetLogo: () => request<null>('/api/admin/settings/logo',{method:'DELETE'}),
  uploadGlobalAvatar: (file:File) => uploadImage('/api/admin/settings/avatar',file,'avatar'),
  resetGlobalAvatar: () => request<null>('/api/admin/settings/avatar',{method:'DELETE'}),
  uploadUserAvatar: (id:string,file:File) => uploadImage(`/api/admin/users/${id}/avatar`,file,'avatar'),
  resetUserAvatar: (id:string) => request<null>(`/api/admin/users/${id}/avatar`,{method:'DELETE'}),
  uploadMyAvatar: (file:File) => uploadImage('/api/me/avatar',file,'avatar'),
  resetMyAvatar: () => request<null>('/api/me/avatar',{method:'DELETE'}),
  sqliteBackup: () => download('/api/admin/backups/sqlite'),
  inspectSQLiteBackup: async (file:File) => {
    const headers=new Headers({Accept:'application/json','Content-Type':'application/vnd.sqlite3'})
    const token=storedToken();if(token)headers.set('Authorization',`Bearer ${token}`)
    const response=await fetch('/api/admin/backups/sqlite/inspect',{method:'POST',headers,body:file})
    const payload=await response.json().catch(()=>({error:response.statusText})) as BackupInspection&{error?:string}
    if(!response.ok)throw new APIError(payload.error||'Backup inspection failed',response.status,payload as unknown as Record<string,unknown>)
    return payload
  },
  managedBackups: () => request<BackupArtifact[]>('/api/admin/backups'),
  createManagedBackup: () => request<BackupArtifact>('/api/admin/backups',{method:'POST'}),
  downloadManagedBackup: (name:string) => download(`/api/admin/backups/${encodeURIComponent(name)}`),
  deleteManagedBackup: (name:string) => request<null>(`/api/admin/backups/${encodeURIComponent(name)}`,{method:'DELETE'}),
  restoreStatus: () => request<{pending:boolean;interval_seconds:number;retention:number}>('/api/admin/backups/restore'),
  stageRestore: async (file:File) => {
    const headers=new Headers({Accept:'application/json','Content-Type':'application/vnd.sqlite3'});const token=storedToken();if(token)headers.set('Authorization',`Bearer ${token}`)
    const response=await fetch('/api/admin/backups/restore',{method:'POST',headers,body:file});const payload=await response.json().catch(()=>({error:response.statusText})) as {pending?:boolean;restart_required?:boolean;inspection?:BackupInspection;error?:string}
    if(!response.ok)throw new APIError(payload.error||'Restore staging failed',response.status,payload as Record<string,unknown>);return payload as {pending:true;restart_required:true;inspection:BackupInspection}
  },
  cancelRestore: () => request<null>('/api/admin/backups/restore',{method:'DELETE'}),
  infrastructure: () => request<Infrastructure>('/api/admin/infrastructure'),
  userPresence: () => request<PresenceSnapshot>('/api/admin/presence'),
  liveConnections: () => request<LiveConnectionSnapshot>('/api/admin/connections'),
  containConnection: (key:string) => request<ConnectionContainment>('/api/admin/connections/contain',{method:'POST',body:JSON.stringify({key})}),
  dashboardLayout: () => request<DashboardLayout>('/api/admin/preferences/dashboard'),
  saveDashboardLayout: (layout:DashboardLayout) => request<DashboardLayout>('/api/admin/preferences/dashboard',{method:'PUT',body:JSON.stringify(layout)}),
  diagnostics: () => request<Diagnostics>('/api/admin/diagnostics'),
  supportBundle: () => download('/api/admin/support-bundle'),
  clusterState: () => request<ClusterState>('/api/admin/cluster'),
  serviceCommands: () => request<ServiceCommand[]>('/api/admin/infrastructure/commands'),
  reconcileHBBS: () => request<ServiceCommand>('/api/admin/infrastructure/commands',{method:'POST',body:JSON.stringify({service:'hbbs',target_instance:'*',type:'reconcile_auth'})}),
  addressBooks: () => request<AddressBook[]>('/api/address-books'),
  createAddressBook: (input:{name:string;kind:'personal'|'shared'}) => request<AddressBook>('/api/address-books',{method:'POST',body:JSON.stringify(input)}),
  updateAddressBook: (id:string,input:Record<string,unknown>) => request<AddressBook>(`/api/address-books/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
  deleteAddressBook: (id:string) => request<null>(`/api/address-books/${id}`,{method:'DELETE'}),
  addressBookEntries: (id:string) => request<AddressBookEntry[]>(`/api/address-books/${id}/entries`),
  createAddressBookEntry: (id:string,input:Record<string,unknown>) => request<AddressBookEntry>(`/api/address-books/${id}/entries`,{method:'POST',body:JSON.stringify(input)}),
  updateAddressBookEntry: (bookID:string,entryID:string,input:Record<string,unknown>) => request<AddressBookEntry>(`/api/address-books/${bookID}/entries/${entryID}`,{method:'PATCH',body:JSON.stringify(input)}),
  deleteAddressBookEntry: (bookID:string,entryID:string) => request<null>(`/api/address-books/${bookID}/entries/${entryID}`,{method:'DELETE'}),
  addressBookGrants: (id:string) => request<AddressBookGrant[]>(`/api/address-books/${id}/grants`),
  putAddressBookGrant: (id:string,input:{subject_type:'user'|'user_group';subject_id:string;permission:'read'|'write'}) => request<AddressBookGrant>(`/api/address-books/${id}/grants`,{method:'PUT',body:JSON.stringify(input)}),
  deleteAddressBookGrant: (bookID:string,grantID:string) => request<null>(`/api/address-books/${bookID}/grants/${grantID}`,{method:'DELETE'}),
  relayServers: () => request<RelayServer[]>('/api/admin/relay-servers'),
  createRelayServer: (input:Record<string,unknown>) => request<RelayServer>('/api/admin/relay-servers',{method:'POST',body:JSON.stringify(input)}),
  updateRelayServer: (id:string,input:Record<string,unknown>) => request<RelayServer>(`/api/admin/relay-servers/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
  deleteRelayServer: (id:string) => request<null>(`/api/admin/relay-servers/${id}`,{method:'DELETE'}),
  relayMetrics: (id:string,hours=24) => request<RelayMetric[]>(`/api/admin/relay-servers/${id}/metrics?hours=${hours}`),
  aclRules: () => request<ACLRule[]>('/api/admin/acl'),
  evaluateACL: (input:{user_id:string;target_id:string;permission:string}) => request<ACLEvaluation>('/api/admin/acl/evaluate',{method:'POST',body:JSON.stringify(input)}),
  createACLRule: (input:Record<string,unknown>) => request<ACLRule>('/api/admin/acl',{method:'POST',body:JSON.stringify(input)}),
  updateACLRule: (id:string,input:Record<string,unknown>) => request<ACLRule>(`/api/admin/acl/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
  deleteACLRule: (id:string) => request<null>(`/api/admin/acl/${id}`,{method:'DELETE'}),
  automationRules: () => request<AutomationRule[]>('/api/admin/automation/rules'),
  automationRuns: (ruleID='') => request<AutomationRun[]>(`/api/admin/automation/runs${ruleID?`?rule_id=${encodeURIComponent(ruleID)}`:''}`),
  createAutomationRule: (input:Record<string,unknown>) => request<AutomationRule>('/api/admin/automation/rules',{method:'POST',body:JSON.stringify(input)}),
  updateAutomationRule: (id:string,input:Record<string,unknown>) => request<AutomationRule>(`/api/admin/automation/rules/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
  deleteAutomationRule: (id:string) => request<null>(`/api/admin/automation/rules/${id}`,{method:'DELETE'}),
  strategies: () => request<Strategy[]>('/api/admin/strategies'),
  strategySchema: () => request<StrategySettingDefinition[]>('/api/admin/strategies/schema'),
  evaluateStrategy: (device_id:string) => request<StrategyEvaluation>('/api/admin/strategies/evaluate',{method:'POST',body:JSON.stringify({device_id})}),
  createStrategy: (input:Record<string,unknown>) => request<Strategy>('/api/admin/strategies',{method:'POST',body:JSON.stringify(input)}),
  updateStrategy: (id:string,input:Record<string,unknown>) => request<Strategy>(`/api/admin/strategies/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
	deleteStrategy: (id:string) => request<null>(`/api/admin/strategies/${id}`,{method:'DELETE'}),
	roles: () => request<RoleDefinition[]>('/api/admin/roles'),
	permissions: () => request<string[]>('/api/admin/permissions'),
	createRole: (input:Record<string,unknown>) => request<RoleDefinition>('/api/admin/roles',{method:'POST',body:JSON.stringify(input)}),
	updateRole: (id:string,input:Record<string,unknown>) => request<RoleDefinition>(`/api/admin/roles/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
	deleteRole: (id:string) => request<null>(`/api/admin/roles/${id}`,{method:'DELETE'}),
	webhooks: () => request<{webhooks:Webhook[];event_types:string[]}>('/api/admin/webhooks'),
	createWebhook: (input:Record<string,unknown>) => request<{webhook:Webhook;secret:string}>('/api/admin/webhooks',{method:'POST',body:JSON.stringify(input)}),
	updateWebhook: (id:string,input:Record<string,unknown>) => request<Webhook>(`/api/admin/webhooks/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
	deleteWebhook: (id:string) => request<null>(`/api/admin/webhooks/${id}`,{method:'DELETE'}),
	webhookDeliveries: (id:string) => request<WebhookDelivery[]>(`/api/admin/webhooks/${id}/deliveries`),
	clientProfiles: () => request<ClientProfile[]>('/api/admin/client-profiles'),
	createClientProfile: (input:Record<string,unknown>) => request<ClientProfile>('/api/admin/client-profiles',{method:'POST',body:JSON.stringify(input)}),
	updateClientProfile: (id:string,input:Record<string,unknown>) => request<ClientProfile>(`/api/admin/client-profiles/${id}`,{method:'PATCH',body:JSON.stringify(input)}),
	deleteClientProfile: (id:string) => request<null>(`/api/admin/client-profiles/${id}`,{method:'DELETE'}),
	clientProfileBundle: (id:string) => request<ClientProfileBundle>(`/api/admin/client-profiles/${id}/bundle`),
	clientProfileAssignments: () => request<ClientProfileAssignment[]>('/api/admin/client-profile-assignments'),
	createClientProfileAssignment: (input:Record<string,unknown>) => request<ClientProfileAssignment>('/api/admin/client-profile-assignments',{method:'POST',body:JSON.stringify(input)}),
	deleteClientProfileAssignment: (id:string) => request<null>(`/api/admin/client-profile-assignments/${id}`,{method:'DELETE'}),
	clientBuilds: () => request<ClientBuild[]>('/api/admin/client-builds'),
	builderWorkers: () => request<BuilderWorker[]>('/api/admin/builder-workers'),
	createClientBuild: (input:Record<string,unknown>) => request<ClientBuild>('/api/admin/client-builds',{method:'POST',body:JSON.stringify(input)}),
	cancelClientBuild: (id:string) => request<ClientBuild>(`/api/admin/client-builds/${id}/cancel`,{method:'POST'}),
	retryClientBuild: (id:string) => request<ClientBuild>(`/api/admin/client-builds/${id}/retry`,{method:'POST'}),
	clientBuildArtifact: (id:string) => download(`/api/admin/client-builds/${id}/artifact`),
}
