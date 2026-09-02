<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { Archive, Boxes, Download, Layers3, MonitorCheck, MonitorOff, Pencil, RefreshCw, RotateCcw, Search, Trash2, Upload, X } from 'lucide-vue-next'
import { api } from '../api'
import { t, tr } from '../preferences'
import type { Device, ManagedGroup } from '../types'

const records = ref<Device[]>([])
const groups = ref<ManagedGroup[]>([])
const loading = ref(true)
const error = ref('')
const selected = ref<Device>()
const saving = ref(false)
const search = ref('')
const status = ref<'all' | 'online' | 'offline' | 'archived'>('all')
const groupFilter = ref('')
const checked = ref<string[]>([])
const bulkOpen = ref(false)
const bulk = reactive({ group_id: '__keep__', add_tags: '', remove_tags: '' })
const refreshedAt = ref<Date>()
const importInput = ref<HTMLInputElement>()
let refreshTimer: ReturnType<typeof setInterval> | undefined
const form = reactive({ alias: '', group_id: '', tags: '' })
const deviceGroups = computed(() => groups.value.filter(group => group.kind === 'device'))
const activeRecords = computed(() => records.value.filter(device => !device.archived_at))
const onlineCount = computed(() => activeRecords.value.filter(device => device.online).length)
const offlineCount = computed(() => activeRecords.value.length - onlineCount.value)
const archivedCount = computed(() => records.value.filter(device => !!device.archived_at).length)
const filtered = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return records.value.filter(device => {
    if (status.value === 'archived' ? !device.archived_at : !!device.archived_at) return false
    if ((status.value === 'online' || status.value === 'offline') && device.online !== (status.value === 'online')) return false
    if (groupFilter.value && device.group_id !== groupFilter.value) return false
    return !needle || `${device.rustdesk_id} ${device.hostname} ${device.alias} ${device.platform} ${device.version} ${device.username} ${device.cpu} ${device.last_seen_ip} ${(device.tags || []).join(' ')}`.toLowerCase().includes(needle)
  })
})
const allVisibleChecked=computed(()=>filtered.value.length>0&&filtered.value.every(device=>checked.value.includes(device.rustdesk_id)))

function groupName(id: string) { return deviceGroups.value.find(group => group.id === id)?.name || '—' }
async function load() {
  loading.value = true
  error.value = ''
  try {
    const [devices, archived, allGroups] = await Promise.all([api.devices(),api.devices(true), api.groups()])
    records.value = [...devices,...archived].map(device => ({ ...device, tags: device.tags || [] }))
    groups.value = allGroups
    refreshedAt.value = new Date()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : tr('Не удалось загрузить устройства','Unable to load devices')
  } finally { loading.value = false }
}
function edit(device: Device) {
  selected.value = device
  Object.assign(form, { alias: device.alias, group_id: device.group_id, tags: (device.tags || []).join(', ') })
}
function toggleVisible(){const visible=filtered.value.map(device=>device.rustdesk_id);checked.value=allVisibleChecked.value?checked.value.filter(id=>!visible.includes(id)):[...new Set([...checked.value,...visible])]}
async function applyBulk(){if(!checked.value.length)return;saving.value=true;error.value='';try{const input:{ids:string[];group_id?:string;add_tags:string[];remove_tags:string[]}={ids:checked.value,add_tags:bulk.add_tags.split(',').map(value=>value.trim()).filter(Boolean),remove_tags:bulk.remove_tags.split(',').map(value=>value.trim()).filter(Boolean)};if(bulk.group_id!=='__keep__')input.group_id=bulk.group_id;await api.bulkUpdateDevices(input);checked.value=[];bulkOpen.value=false;Object.assign(bulk,{group_id:'__keep__',add_tags:'',remove_tags:''});await load()}catch(reason){error.value=reason instanceof Error?reason.message:tr('Не удалось обновить устройства','Unable to update devices')}finally{saving.value=false}}
async function exportCSV(){try{const result=await api.exportDevices();const url=URL.createObjectURL(result.blob);const link=document.createElement('a');link.href=url;link.download=result.filename;document.body.appendChild(link);link.click();link.remove();URL.revokeObjectURL(url)}catch(reason){error.value=reason instanceof Error?reason.message:tr('Не удалось экспортировать устройства','Unable to export devices')}}
async function importCSV(event:Event){const input=event.target as HTMLInputElement;const file=input.files?.[0];input.value='';if(!file)return;saving.value=true;error.value='';try{const result=await api.importDevices(file);await load();window.alert(tr(`Импортировано устройств: ${result.imported}`,`Imported devices: ${result.imported}`))}catch(reason){error.value=reason instanceof Error?reason.message:tr('Не удалось импортировать устройства','Unable to import devices')}finally{saving.value=false}}
async function archiveSelected(){if(!selected.value||!window.confirm(tr(`Архивировать ${selected.value.rustdesk_id}?`,`Archive ${selected.value.rustdesk_id}?`)))return;saving.value=true;try{await api.archiveDevice(selected.value.rustdesk_id);selected.value=undefined;await load()}catch(reason){error.value=reason instanceof Error?reason.message:'Archive failed'}finally{saving.value=false}}
async function restoreSelected(){if(!selected.value)return;saving.value=true;try{await api.restoreDevice(selected.value.rustdesk_id);selected.value=undefined;await load()}catch(reason){error.value=reason instanceof Error?reason.message:'Restore failed'}finally{saving.value=false}}
async function deleteSelected(){if(!selected.value||!window.confirm(tr(`Окончательно удалить ${selected.value.rustdesk_id}? Устройство появится снова при новом heartbeat.`,`Permanently delete ${selected.value.rustdesk_id}? It will reappear after a new heartbeat.`)))return;saving.value=true;try{await api.deleteArchivedDevice(selected.value.rustdesk_id);selected.value=undefined;await load()}catch(reason){error.value=reason instanceof Error?reason.message:'Delete failed'}finally{saving.value=false}}
async function save() {
  if (!selected.value) return
  saving.value = true
  error.value = ''
  try {
    await api.updateDevice(selected.value.rustdesk_id, { alias: form.alias, group_id: form.group_id, tags: form.tags.split(',').map(value => value.trim()).filter(Boolean) })
    selected.value = undefined
    await load()
  } catch (reason) { error.value = reason instanceof Error ? reason.message : tr('Не удалось обновить устройство','Unable to update device') }
  finally { saving.value = false }
}
function lastSeen(value: string) {
  const date = new Date(value)
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) return tr(`${seconds} сек. назад`,`${seconds}s ago`)
  if (seconds < 3600) return tr(`${Math.floor(seconds / 60)} мин. назад`,`${Math.floor(seconds / 60)}m ago`)
  if (seconds < 86400) return tr(`${Math.floor(seconds / 3600)} ч. назад`,`${Math.floor(seconds / 3600)}h ago`)
  return date.toLocaleString()
}
onMounted(() => {
  void load()
  refreshTimer = setInterval(() => { if (!selected.value && !saving.value) void load() }, 30_000)
})
onUnmounted(() => { if (refreshTimer) clearInterval(refreshTimer) })
</script>

<template>
  <section class="section-heading"><div><p class="eyebrow">Device inventory</p><h2>{{ t('managedDevices') }}</h2><p>{{ t('deviceHelp') }}<small v-if="refreshedAt" class="row-subtle">{{tr('Обновлено','Updated')}} {{refreshedAt.toLocaleTimeString()}} · {{tr('автоматически каждые 30 секунд','automatically every 30 seconds')}}</small></p></div><div class="heading-actions"><input ref="importInput" class="visually-hidden" type="file" accept=".csv,text/csv" @change="importCSV"/><button class="secondary-button" :disabled="saving" @click="importInput?.click()"><Upload :size="16"/>{{tr('Импорт CSV','Import CSV')}}</button><button class="secondary-button" @click="exportCSV"><Download :size="16"/>{{tr('Экспорт CSV','Export CSV')}}</button><button class="secondary-button" :disabled="loading" @click="load"><RefreshCw :size="16" :class="{spinning:loading}"/>{{ t('refresh') }}</button></div></section>
  <p v-if="error" class="alert-error">{{ error }}</p>
  <section class="device-summary">
    <div><Boxes :size="18"/><span><strong>{{activeRecords.length}}</strong>{{tr('Активных','Active')}}</span></div>
    <div class="online"><MonitorCheck :size="18"/><span><strong>{{onlineCount}}</strong>{{tr('В сети','Online')}}</span></div>
    <div class="offline"><MonitorOff :size="18"/><span><strong>{{offlineCount}}</strong>{{tr('Офлайн','Offline')}}</span></div>
    <div><Archive :size="18"/><span><strong>{{archivedCount}}</strong>{{tr('В архиве','Archived')}}</span></div>
  </section>
  <section class="panel table-panel">
    <div v-if="checked.length" class="bulk-bar"><strong>{{tr('Выбрано','Selected')}}: {{checked.length}}</strong><span>{{tr('Массовое назначение группы и добавление тегов','Bulk group assignment and tag addition')}}</span><button class="secondary-button" @click="checked=[]">{{tr('Снять выбор','Clear selection')}}</button><button class="primary-button compact" @click="bulkOpen=true"><Layers3 :size="15"/>{{tr('Изменить','Change')}}</button></div>
    <div class="table-toolbar device-toolbar">
      <div class="search-box"><Search :size="17"/><input v-model="search" :placeholder="tr('ID, имя, платформа или тег','ID, name, platform, or tag')"/></div>
      <select v-model="status"><option value="all">{{tr('Активные устройства','Active devices')}}</option><option value="online">{{tr('В сети','Online')}}</option><option value="offline">{{tr('Офлайн','Offline')}}</option><option value="archived">{{tr('Архив','Archive')}}</option></select>
      <select v-model="groupFilter"><option value="">{{tr('Все группы','All groups')}}</option><option v-for="group in deviceGroups" :key="group.id" :value="group.id">{{group.name}}</option></select>
      <span class="filter-count">{{filtered.length}} {{tr('из','of')}} {{records.length}}</span>
    </div>
    <div class="table-scroll"><table><thead><tr><th class="select-cell"><input type="checkbox" :checked="allVisibleChecked" :aria-label="tr('Выбрать видимые устройства','Select visible devices')" @change="toggleVisible"/></th><th>{{ t('rustdeskId') }}</th><th>{{ t('hostname') }}</th><th>{{ t('platform') }}</th><th>{{tr('Группа','Group')}}</th><th>{{ t('lastSeen') }}</th><th>{{ t('status') }}</th><th></th></tr></thead><tbody>
      <tr v-if="loading"><td colspan="8" class="empty-cell">{{ t('loading') }}</td></tr><tr v-else-if="!filtered.length"><td colspan="8" class="empty-cell"><Boxes :size="22"/><br/>{{ t('noRecords') }}</td></tr>
      <tr v-for="device in filtered" :key="device.rustdesk_id" :class="{selected:checked.includes(device.rustdesk_id)}"><td class="select-cell"><input v-model="checked" type="checkbox" :value="device.rustdesk_id" :aria-label="tr(`Выбрать ${device.rustdesk_id}`,`Select ${device.rustdesk_id}`)"/></td><td><strong>{{device.rustdesk_id}}</strong><small v-if="device.tags?.length" class="row-detail">{{device.tags.join(', ')}}</small></td><td>{{device.alias || device.hostname || '—'}}<small v-if="device.alias && device.hostname" class="row-subtle">{{device.hostname}}</small></td><td>{{device.platform || '—'}}<small v-if="device.version" class="row-subtle">{{device.version}}</small></td><td>{{groupName(device.group_id)}}</td><td :title="new Date(device.last_seen).toLocaleString()">{{lastSeen(device.last_seen)}}<small v-if="device.last_seen_ip" class="row-subtle">{{device.last_seen_ip}}</small></td><td><span class="status-text" :class="{disabled:!device.online}"><span></span>{{device.online?t('online'):tr('Офлайн','Offline')}}</span></td><td><button class="icon-btn" :aria-label="tr('Редактировать устройство','Edit device')" @click="edit(device)"><Pencil :size="15"/></button></td></tr>
    </tbody></table></div>
  </section>
  <div v-if="selected" class="modal-backdrop" @click.self="selected=undefined"><form class="modal-card" @submit.prevent="save"><div class="modal-heading"><div><p class="overline">{{selected.rustdesk_id}}</p><h3>{{tr('Управление устройством','Device management')}}</h3></div><button type="button" class="icon-btn" @click="selected=undefined"><X :size="20"/></button></div><div class="device-facts"><div><span>{{tr('Имя устройства','Device name')}}</span><strong>{{selected.hostname||'—'}}</strong></div><div><span>{{tr('Пользователь ОС','OS user')}}</span><strong>{{selected.username||'—'}}</strong></div><div><span>{{tr('ОС / версия клиента','OS / client version')}}</span><strong>{{selected.platform||'—'}} · {{selected.version||'—'}}</strong></div><div><span>{{tr('Последний IP','Last IP')}}</span><strong>{{selected.last_seen_ip||'—'}}</strong></div><div><span>{{tr('Процессор','Processor')}}</span><strong>{{selected.cpu||'—'}}</strong></div><div><span>{{tr('Память','Memory')}}</span><strong>{{selected.memory||'—'}}</strong></div></div><template v-if="!selected.archived_at"><label><span>{{tr('Псевдоним','Alias')}}</span><input v-model="form.alias" maxlength="128"/></label><label><span>{{tr('Группа устройств','Device group')}}</span><select v-model="form.group_id"><option value="">{{tr('Без группы','No group')}}</option><option v-for="group in deviceGroups" :key="group.id" :value="group.id">{{group.name}}</option></select></label><label><span>{{tr('Теги через запятую','Comma-separated tags')}}</span><input v-model="form.tags"/></label></template><p v-else class="muted-text">{{tr('Архивировано','Archived')}}: {{new Date(selected.archived_at).toLocaleString()}}</p><div class="modal-actions"><button v-if="selected.archived_at" type="button" class="secondary-button" :disabled="saving" @click="restoreSelected"><RotateCcw :size="15"/>{{tr('Восстановить','Restore')}}</button><button v-if="selected.archived_at" type="button" class="danger-button" :disabled="saving" @click="deleteSelected"><Trash2 :size="15"/>{{tr('Удалить навсегда','Delete permanently')}}</button><button v-else type="button" class="secondary-button" :disabled="saving" @click="archiveSelected"><Archive :size="15"/>{{tr('В архив','Archive')}}</button><button type="button" class="secondary-button" @click="selected=undefined">{{tr('Отмена','Cancel')}}</button><button v-if="!selected.archived_at" class="primary-button compact" :disabled="saving">{{saving?tr('Сохранение…','Saving…'):tr('Сохранить','Save')}}</button></div></form></div>
  <div v-if="bulkOpen" class="modal-backdrop" @click.self="bulkOpen=false"><form class="modal-card" @submit.prevent="applyBulk"><div class="modal-heading"><div><p class="overline">{{checked.length}} {{tr('устройств','devices')}}</p><h3>{{tr('Массовое изменение','Bulk update')}}</h3></div><button type="button" class="icon-btn" @click="bulkOpen=false"><X :size="20"/></button></div><label><span>{{tr('Группа устройств','Device group')}}</span><select v-model="bulk.group_id"><option value="__keep__">{{tr('Не изменять группу','Keep group')}}</option><option value="">{{tr('Убрать из группы','Remove from group')}}</option><option v-for="group in deviceGroups" :key="group.id" :value="group.id">{{group.name}}</option></select></label><label><span>{{tr('Добавить теги через запятую','Add comma-separated tags')}}</span><input v-model="bulk.add_tags" maxlength="1024" placeholder="office, managed"/></label><label><span>{{tr('Удалить теги через запятую','Remove comma-separated tags')}}</span><input v-model="bulk.remove_tags" maxlength="1024" placeholder="legacy, temporary"/></label><p class="muted-text">{{tr('Остальные существующие теги сохраняются.','All other existing tags are preserved.')}}</p><div class="modal-actions"><button type="button" class="secondary-button" @click="bulkOpen=false">{{tr('Отмена','Cancel')}}</button><button class="primary-button compact" :disabled="saving">{{saving?tr('Сохранение…','Saving…'):tr('Применить','Apply')}}</button></div></form></div>
</template>

<style scoped>
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
