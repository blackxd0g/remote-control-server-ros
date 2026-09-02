<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { Activity, Bell, BookOpenCheck, BookUser, Boxes, Cable, CheckCheck, ChevronDown, CircleGauge, DatabaseBackup, FileClock, Globe2, Group, KeyRound, Laptop, LifeBuoy, LogOut, Menu, MoonStar, RadioTower, Send, ServerCog, Settings, ShieldCheck, Users, Workflow, X } from 'lucide-vue-next'
import BrandMark from './BrandMark.vue'
import { api } from '../api'
import { auth } from '../auth'
import { preferences, t, tr, type Locale, type Theme } from '../preferences'
import type { Notification } from '../types'
import { appVersion } from '../version'

const route = useRoute()
const router = useRouter()
const menuOpen = ref(false)
const accountOpen = ref(false)
const notificationOpen = ref(false)
const notifications = ref<Notification[]>([])
const unread = computed(() => notifications.value.filter(value => !value.read_at).length)
let notificationTimer: number | undefined
const titleKeys: Record<string, Parameters<typeof t>[0]> = { Dashboard:'dashboard',Users:'users',Roles:'roles',Devices:'devices',Groups:'groups',Backups:'backups',Support:'support','Address Books':'addressBooks','Access Control':'accessControl',Strategies:'strategies',Sessions:'sessions',Connections:'connections',Audit:'audit','Relay Servers':'relayServers',Webhooks:'webhooks',Automation:'automation','Client Profiles':'clientProfiles',Settings:'settings' }
const title = computed(() => t(titleKeys[String(route.meta.title || 'Dashboard')] || 'dashboard'))
const initials = computed(() => (auth.state.user?.display_name || auth.state.user?.username || 'A').slice(0, 2).toUpperCase())
const items = [
  { to: '/admin', label: 'dashboard' as const, icon: CircleGauge, permission:'admin.portal' }, { to: '/admin/users', label: 'users' as const, icon: Users, permission:'users.read' },
	{ to: '/admin/roles', label: 'roles' as const, icon: KeyRound, permission:'roles.read' },
  { to: '/admin/devices', label: 'devices' as const, icon: Boxes, permission:'devices.read' }, { to: '/admin/groups', label: 'groups' as const, icon: Group, permission:'groups.read' },
  { to: '/admin/address-books', label: 'addressBooks' as const, icon: BookUser, permission:'address_books.read' }, { to: '/admin/access-control', label: 'accessControl' as const, icon: ShieldCheck, permission:'acl.read' },
  { to: '/admin/strategies', label: 'strategies' as const, icon: BookOpenCheck, permission:'strategies.read' }, { to: '/admin/sessions', label: 'sessions' as const, icon: Activity, permission:'sessions.read' },
  { to: '/admin/connections', label: 'connections' as const, icon: RadioTower, permission:'audit.read' },
  { to: '/admin/audit', label: 'audit' as const, icon: FileClock, permission:'audit.read' }, { to: '/admin/relay-servers', label: 'relayServers' as const, icon: Cable, permission:'relays.read' },
  { to: '/admin/webhooks', label: 'webhooks' as const, icon: Send, permission:'webhooks.read' },
  { to: '/admin/automation', label: 'automation' as const, icon: Workflow, permission:'automation.read' },
  { to: '/admin/client-profiles', label: 'clientProfiles' as const, icon: Laptop, permission:'client_profiles.read' },
  { to: '/admin/settings', label: 'settings' as const, icon: Settings, permission:'settings.read' },
  { to: '/admin/backups', label: 'backups' as const, icon: DatabaseBackup, permission:'backup.read' },
  { to: '/admin/support', label: 'support' as const, icon: LifeBuoy, permission:'infrastructure.read' },
]
const visibleItems=computed(()=>items.filter(item=>!('permission' in item)||!item.permission||auth.can(item.permission)))

async function logout() {
  await auth.logout()
  await router.push('/admin/login')
}
async function loadNotifications() {
  if (!auth.can('audit.read')) return
  try { notifications.value = (await api.notifications()).notifications } catch { /* keep navigation available */ }
}
async function readNotification(value:Notification) {
  if (!value.read_at) await api.markNotificationRead(value.id)
  notifications.value = notifications.value.filter(item => item.id !== value.id)
}
async function readAllNotifications() {
  await api.markAllNotificationsRead()
  notifications.value=[]
}
function notificationCopy(value:Notification) {
  const copies:Record<string,[string,string,string,string]>={
    login_failed:['Неудачный вход','Failed sign-in','Попытка входа была отклонена','A sign-in attempt was denied'],
    device_identity_mismatch:['Несовпадение идентификатора устройства','Device identity mismatch','Клиент предъявил неожиданный идентификатор устройства','A client presented an unexpected device identity'],
    user_registration:['Регистрация ожидает решения','Registration pending','Новая регистрация пользователя требует подтверждения','A new user registration requires approval'],
    mfa_disabled:['Изменена многофакторная аутентификация','Multi-factor authentication changed','Многофакторная аутентификация была отключена','Multi-factor authentication was disabled'],
    mfa_admin_reset:['Изменена многофакторная аутентификация','Multi-factor authentication changed','Администратор сбросил многофакторную аутентификацию','An administrator reset multi-factor authentication'],
    api_token_create:['Создан API-токен','API token created','Создан новый API-токен развёртывания','A new deployment API token was created'],
    server_control_command:['Команда сервера поставлена в очередь','Server command queued','Аутентифицированная команда управления сервером поставлена в очередь','An authenticated server-control command was queued'],
  }
  const copy=copies[value.type]
  return copy?{title:tr(copy[0],copy[1]),message:tr(copy[2],copy[3])}:{title:value.title,message:value.message}
}
function reloadPage(){window.location.reload()}
onMounted(()=>{void loadNotifications();notificationTimer=window.setInterval(loadNotifications,30_000)})
onBeforeUnmount(()=>{if(notificationTimer!==undefined)window.clearInterval(notificationTimer)})
</script>

<template>
  <div class="app-layout">
    <div v-if="menuOpen" class="sidebar-backdrop" @click="menuOpen = false"></div>
    <aside class="sidebar" :class="{ open: menuOpen }">
      <div class="brand-row">
        <button class="brand-refresh" type="button" :aria-label="tr('Обновить страницу','Refresh page')" @click="reloadPage">
          <BrandMark />
          <div class="brand-copy"><strong>Remote Control</strong><span>Server</span></div>
        </button>
        <button class="icon-btn mobile-close" :aria-label="tr('Закрыть меню','Close menu')" @click="menuOpen = false"><X :size="20" /></button>
      </div>
      <p class="nav-label">{{tr('Рабочая область','Workspace')}}</p>
      <nav :aria-label="tr('Основная навигация','Primary navigation')">
        <RouterLink v-for="item in visibleItems" :key="item.to" :to="item.to" :title="t(item.label)" @click="menuOpen = false">
          <component :is="item.icon" :size="18" stroke-width="1.8" />
          <span>{{ t(item.label) }}</span>
        </RouterLink>
      </nav>
    </aside>
    <main class="main-area">
      <header class="topbar">
        <button class="icon-btn menu-button" :aria-label="tr('Открыть меню','Open menu')" @click="menuOpen = true"><Menu :size="21" /></button>
        <div><p>{{ t('administration') }}</p><h1>{{ title }}</h1></div>
        <div class="top-spacer"></div>
        <div class="quick-preferences"><label><MoonStar :size="15"/><select :value="preferences.state.theme" :aria-label="t('theme')" @change="preferences.setTheme(($event.target as HTMLSelectElement).value as Theme)"><option value="system">{{t('system')}}</option><option value="dark">{{t('dark')}}</option><option value="light">{{t('light')}}</option></select></label><label><Globe2 :size="15"/><select :value="preferences.state.locale" :aria-label="t('language')" @change="preferences.setLocale(($event.target as HTMLSelectElement).value as Locale)"><option value="ru">RU</option><option value="en">EN</option></select></label></div>
        <div v-if="auth.can('audit.read')" class="notification-wrap"><button class="icon-btn notification-button" :aria-label="tr('Уведомления','Notifications')" @click="notificationOpen=!notificationOpen;accountOpen=false"><Bell :size="19"/><span v-if="unread">{{unread>99?'99+':unread}}</span></button><section v-if="notificationOpen" class="notification-panel"><header><div><strong>{{tr('Уведомления','Notifications')}}</strong><small>{{unread}} {{tr('непрочитано','unread')}}</small></div><button v-if="unread" class="icon-btn" :title="tr('Отметить все прочитанными','Mark all as read')" @click="readAllNotifications"><CheckCheck :size="17"/></button></header><div class="notification-list"><button v-for="value in notifications" :key="value.id" :class="['notification-item',value.severity,{read:!!value.read_at}]" @click="readNotification(value)"><span></span><div><strong>{{notificationCopy(value).title}}</strong><p>{{notificationCopy(value).message}}</p><small>{{new Date(value.created_at).toLocaleString()}}{{value.resource?` · ${value.resource}`:''}}</small></div></button><p v-if="!notifications.length" class="notification-empty">{{tr('Новых уведомлений нет','No notifications')}}</p></div></section></div>
        <div class="account-wrap">
          <button class="account-button" @click="accountOpen = !accountOpen">
            <span class="avatar">{{ initials }}</span>
            <span class="account-copy"><strong>{{ auth.state.user?.display_name || auth.state.user?.username }}</strong><small>{{ auth.state.user?.role }}</small></span>
            <ChevronDown :size="16" />
          </button>
          <div v-if="accountOpen" class="account-menu">
            <p>{{ auth.state.user?.email || tr('Локальная учётная запись','Local account') }}</p>
            <button @click="logout"><LogOut :size="16" />{{ t('signOut') }}</button>
          </div>
        </div>
      </header>
      <div class="page-content"><RouterView /></div>
      <footer><ServerCog :size="15" /> Remote Control Server · v{{ appVersion }} · Compatible with the RustDesk client protocol · <a href="https://boosty.to/blackxdog" target="_blank" rel="noopener noreferrer">created by blackxdog</a></footer>
    </main>
  </div>
</template>
