import { reactive } from 'vue'

export type Locale = 'ru' | 'en'
export type Theme = 'system' | 'dark' | 'light'

const messages = {
  en: {
    administration: 'Administration', dashboard: 'Dashboard', users: 'Users', roles:'Roles', devices: 'Devices', groups:'Groups', backups:'Backups', support:'Diagnostics',
    addressBooks: 'Address Books', accessControl: 'Access Control', strategies: 'Strategies', sessions: 'Sessions', connections:'Connections',
    audit: 'Audit', relayServers: 'Relay Servers', webhooks:'Webhooks', automation:'Automation', clientProfiles:'Client Profiles', infrastructure: 'Infrastructure', settings: 'Settings', signOut: 'Sign out',
    coreServices: 'Core services', authOnline: 'Authentication online', theme: 'Theme', language: 'Language', system: 'System',
    dark: 'Dark', light: 'Light', refresh: 'Refresh', loading: 'Loading…', noRecords: 'No records yet.',
    managedDevices: 'Managed devices', deviceHelp: 'All RustDesk IDs registered with HBBS appear here after their next heartbeat.',
    rustdeskId: 'RustDesk ID', hostname: 'Hostname', platform: 'Platform', owner: 'Owner', lastSeen: 'Last seen', status: 'Status',
    online: 'Online', addGroup: 'Add group', managedGroups: 'User and device groups', groupHelp: 'Build separate scopes for identities and managed endpoints.',
    groupName: 'Group name', description: 'Description', kind: 'Type', userGroup: 'User group', deviceGroup: 'Device group', create: 'Create', cancel: 'Cancel',
    activeSessions: 'Active sessions', sessionsHelp: 'Review server-side sessions and revoke access immediately.', revoke: 'Revoke',
    deviceId: 'Device ID', expires: 'Expires', ipAddress: 'IP address', auditTrail: 'Audit trail', auditHelp: 'Security and administration events recorded by the platform.',
    event: 'Event', result: 'Result', time: 'Time', actor: 'Actor', nodeHealth: 'Node health', infraHelp: 'Current all-in-one service and datastore status.',
    apiServer: 'API Server', database: 'Database', managedUsers: 'Managed users', runtimeSettings: 'Runtime settings',
    settingsHelp: 'Effective server configuration and local console preferences.', requireLogin: 'Require login', relayServer: 'Relay server',
    tokenTtl: 'Access token TTL', sessionTtl: 'Session TTL', loginProtection: 'Login protection', configuredByEnv: 'Configured through container ENV',
    welcome: 'Good to see you', authProtecting: 'Authentication Core is active and protecting new remote connections.', operational: 'Operational',
    welcomeBack: 'Welcome back', signInIntro: 'Sign in to Remote Control Server.', username: 'Username', password: 'Password', signIn: 'Sign in', signingIn: 'Signing in…',
  },
  ru: {
    administration: 'Администрирование', dashboard: 'Обзор', users: 'Пользователи', roles:'Роли', devices: 'Устройства', groups: 'Группы', backups:'Резервные копии', support:'Диагностика',
    addressBooks: 'Адресные книги', accessControl: 'Контроль доступа', strategies: 'Стратегии', sessions: 'Сессии', connections:'Подключения',
    audit: 'Аудит', relayServers: 'Relay-серверы', webhooks:'Webhooks', automation:'Автоматизация', clientProfiles:'Профили клиентов', infrastructure: 'Инфраструктура', settings: 'Настройки', signOut: 'Выйти',
    coreServices: 'Основные сервисы', authOnline: 'Аутентификация работает', theme: 'Тема', language: 'Язык', system: 'Системная',
    dark: 'Тёмная', light: 'Светлая', refresh: 'Обновить', loading: 'Загрузка…', noRecords: 'Записей пока нет.',
    managedDevices: 'Управляемые устройства', deviceHelp: 'Здесь отображаются все RustDesk ID, зарегистрированные в HBBS после очередного heartbeat.',
    rustdeskId: 'RustDesk ID', hostname: 'Имя устройства', platform: 'Платформа', owner: 'Владелец', lastSeen: 'Последняя активность', status: 'Статус',
    online: 'В сети', addGroup: 'Добавить группу', managedGroups: 'Группы пользователей и устройств', groupHelp: 'Создавайте отдельные области для учётных записей и устройств.',
    groupName: 'Название группы', description: 'Описание', kind: 'Тип', userGroup: 'Группа пользователей', deviceGroup: 'Группа устройств', create: 'Создать', cancel: 'Отмена',
    activeSessions: 'Активные сессии', sessionsHelp: 'Просматривайте серверные сессии и немедленно отзывайте доступ.', revoke: 'Отозвать',
    deviceId: 'ID устройства', expires: 'Истекает', ipAddress: 'IP-адрес', auditTrail: 'Журнал аудита', auditHelp: 'События безопасности и администрирования платформы.',
    event: 'Событие', result: 'Результат', time: 'Время', actor: 'Инициатор', nodeHealth: 'Состояние узла', infraHelp: 'Текущее состояние сервисов all-in-one и базы данных.',
    apiServer: 'API-сервер', database: 'База данных', managedUsers: 'Пользователи', runtimeSettings: 'Настройки среды',
    settingsHelp: 'Фактическая конфигурация сервера и локальные настройки консоли.', requireLogin: 'Обязательный вход', relayServer: 'Relay-сервер',
    tokenTtl: 'Срок access token', sessionTtl: 'Срок сессии', loginProtection: 'Защита входа', configuredByEnv: 'Настраивается через ENV контейнера',
    welcome: 'Рады видеть вас', authProtecting: 'Authentication Core активен и защищает новые удалённые подключения.', operational: 'Работает',
    welcomeBack: 'С возвращением', signInIntro: 'Войдите в Remote Control Server.', username: 'Логин', password: 'Пароль', signIn: 'Войти', signingIn: 'Вход…',
  },
} as const

type MessageKey = keyof typeof messages.en
const savedLocale = localStorage.getItem('art.locale')
const savedTheme = localStorage.getItem('art.theme')
const state = reactive({
  locale: (savedLocale === 'en' || savedLocale === 'ru' ? savedLocale : navigator.language.toLowerCase().startsWith('ru') ? 'ru' : 'en') as Locale,
  theme: (savedTheme === 'light' || savedTheme === 'dark' || savedTheme === 'system' ? savedTheme : 'system') as Theme,
})
const media = matchMedia('(prefers-color-scheme: dark)')

function applyTheme() {
  const resolved = state.theme === 'system' ? (media.matches ? 'dark' : 'light') : state.theme
  document.documentElement.dataset.theme = resolved
  document.documentElement.style.colorScheme = resolved
}

export function setTheme(theme: Theme) { state.theme = theme; localStorage.setItem('art.theme', theme); applyTheme() }
export function setLocale(locale: Locale) { state.locale = locale; localStorage.setItem('art.locale', locale); document.documentElement.lang = locale }
export function t(key: MessageKey): string { return messages[state.locale][key] }
export function tr(ru: string, en: string): string { return state.locale === 'ru' ? ru : en }
export const preferences = { state, setTheme, setLocale, t }

media.addEventListener?.('change', applyTheme)
document.documentElement.lang = state.locale
applyTheme()
