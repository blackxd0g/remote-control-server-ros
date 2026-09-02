import { createRouter, createWebHistory } from 'vue-router'
import { auth } from './auth'
import LoginView from './views/LoginView.vue'
import DashboardView from './views/DashboardView.vue'
import UsersView from './views/UsersView.vue'
import AddressBooksView from './views/AddressBooksView.vue'
import AccessControlView from './views/AccessControlView.vue'
import StrategiesView from './views/StrategiesView.vue'
import RelayServersView from './views/RelayServersView.vue'
import DevicesView from './views/DevicesView.vue'
import GroupsView from './views/GroupsView.vue'
import SessionsView from './views/SessionsView.vue'
import ConnectionsView from './views/ConnectionsView.vue'
import AuditView from './views/AuditView.vue'
import SettingsView from './views/SettingsView.vue'
import AppShell from './components/AppShell.vue'
import RegisterView from './views/RegisterView.vue'
import AccountView from './views/AccountView.vue'
import RolesView from './views/RolesView.vue'
import WebhooksView from './views/WebhooksView.vue'
import ClientProfilesView from './views/ClientProfilesView.vue'
import BackupsView from './views/BackupsView.vue'
import AutomationView from './views/AutomationView.vue'
import SupportView from './views/SupportView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', redirect: '/admin/login' },
    { path: '/admin/login', name: 'admin-login', component: LoginView, meta: { public: true, portal: 'admin' } },
    { path: '/account/login', name: 'account-login', component: LoginView, meta: { public: true, portal: 'account' } },
    { path: '/register', name: 'register', component: RegisterView, meta: { public: true, portal: 'account' } },
    { path: '/account', name: 'account', component: AccountView, meta: { account: true, title: 'Account' } },
    {
      path: '/admin', component: AppShell, meta: { admin: true },
      children: [
        { path: '', name: 'dashboard', component: DashboardView, meta: { title: 'Dashboard' } },
        { path: 'users', name: 'users', component: UsersView, meta: { title: 'Users', permission:'users.read' } },
		{ path: 'roles', name: 'roles', component: RolesView, meta: { title: 'Roles', permission:'roles.read' } },
        { path: 'devices', name: 'devices', component: DevicesView, meta: { title: 'Devices', permission:'devices.read' } },
        { path: 'groups', name: 'groups', component: GroupsView, meta: { title: 'Groups', permission:'groups.read' } },
        { path: 'sessions', name: 'sessions', component: SessionsView, meta: { title: 'Sessions', permission:'sessions.read' } },
        { path: 'connections', name: 'connections', component: ConnectionsView, meta: { title: 'Connections', permission:'audit.read' } },
        { path: 'audit', name: 'audit', component: AuditView, meta: { title: 'Audit', permission:'audit.read' } },
        { path: 'infrastructure', redirect: { name: 'dashboard' } },
        { path: 'settings', name: 'settings', component: SettingsView, meta: { title: 'Settings', permission:'settings.read' } },
        { path: 'backups', name: 'backups', component: BackupsView, meta: { title: 'Backups', permission:'backup.read' } },
        { path: 'support', name: 'support', component: SupportView, meta: { title: 'Support', permission:'infrastructure.read' } },
        { path: 'address-books', name: 'address-books', component: AddressBooksView, meta: { title: 'Address Books', permission:'address_books.read' } },
        { path: 'access-control', name: 'access-control', component: AccessControlView, meta: { title: 'Access Control', permission:'acl.read' } },
        { path: 'strategies', name: 'strategies', component: StrategiesView, meta: { title: 'Strategies', permission:'strategies.read' } },
        { path: 'relay-servers', name: 'relay-servers', component: RelayServersView, meta: { title: 'Relay Servers', permission:'relays.read' } },
		{ path: 'webhooks', name: 'webhooks', component: WebhooksView, meta: { title: 'Webhooks', permission:'webhooks.read' } },
		{ path: 'automation', name: 'automation', component: AutomationView, meta: { title: 'Automation', permission:'automation.read' } },
		{ path: 'client-profiles', name: 'client-profiles', component: ClientProfilesView, meta: { title: 'Client Profiles', permission:'client_profiles.read' } },
      ],
    },
    { path: '/', redirect: '/admin' },
    { path: '/:pathMatch(.*)*', redirect: '/admin' },
  ],
})

router.beforeEach(async (to) => {
  if (!auth.state.ready) await auth.restore()
  if (!to.meta.public && !auth.state.user) return { name: to.meta.account ? 'account-login' : 'admin-login', query: { returnTo: to.fullPath } }
  if (to.meta.admin && !auth.can('admin.portal')) return { name: 'account' }
	if (to.meta.permission && !auth.can(String(to.meta.permission))) return { name:'dashboard' }
  if ((to.name === 'admin-login'||to.name === 'account-login') && auth.state.user) return auth.can('admin.portal')&&to.name==='admin-login'?{name:'dashboard'}:{name:'account'}
})

router.afterEach((to) => {
  document.title = `${String(to.meta.title || 'Console')} · Remote Control Server`
})
