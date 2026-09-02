<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Activity, Cable, CheckCircle2, CircleAlert, Cpu, Database, MemoryStick, RadioTower, RefreshCw, RotateCw, Server, TriangleAlert, Users } from 'lucide-vue-next'
import MetricCard from '../components/MetricCard.vue'
import { api } from '../api'
import { t, tr } from '../preferences'
import type { Diagnostics, Infrastructure, ServiceCommand } from '../types'

const info=ref<Infrastructure|null>(null),error=ref(''),loading=ref(false)
const commands=ref<ServiceCommand[]>([]),commandBusy=ref(false)
const diagnostics=ref<Diagnostics|null>(null)
const history=computed(()=>info.value?.history??[])
function points(values:number[],maximum?:number){if(values.length<2)return '';const max=maximum??Math.max(...values,1);return values.map((value,index)=>`${(index/(values.length-1)*100).toFixed(2)},${(42-Math.min(value/max,1)*38).toFixed(2)}`).join(' ')}
const cpuPoints=computed(()=>points(history.value.map(sample=>sample.cpu_percent),100))
const memoryPoints=computed(()=>points(history.value.map(sample=>sample.memory_bytes)))
const devicePoints=computed(()=>points(history.value.map(sample=>sample.online_devices)))
const relayPoints=computed(()=>points(history.value.map(sample=>sample.relay_connections)))
function formatRate(value=0){if(value<1024)return `${value} B/s`;if(value<1024**2)return `${(value/1024).toFixed(1)} KiB/s`;return `${(value/1024**2).toFixed(1)} MiB/s`}
function formatMemory(value=0){return `${(value/1024/1024).toFixed(1)} MiB`}
function formatUptime(value=0){const days=Math.floor(value/86400),hours=Math.floor(value%86400/3600),minutes=Math.floor(value%3600/60);return days?tr(`${days}д ${hours}ч`,`${days}d ${hours}h`):tr(`${hours}ч ${minutes}м`,`${hours}h ${minutes}m`)}
async function load(){loading.value=true;error.value='';try{[info.value,commands.value,diagnostics.value]=await Promise.all([api.infrastructure(),api.serviceCommands(),api.diagnostics()])}catch(reason){error.value=reason instanceof Error?reason.message:tr('Инфраструктура недоступна','Infrastructure unavailable')}finally{loading.value=false}}
async function reconcile(){commandBusy.value=true;error.value='';try{await api.reconcileHBBS();await load()}catch(reason){error.value=reason instanceof Error?reason.message:tr('Команда не выполнена','Command failed')}finally{commandBusy.value=false}}
let timer:number|undefined
onMounted(()=>{void load();timer=window.setInterval(()=>void load(),10000)})
onUnmounted(()=>{if(timer)window.clearInterval(timer)})
</script>

<template>
  <section class="section-heading"><div><p class="eyebrow">Runtime telemetry</p><h2>{{t('nodeHealth')}}</h2><p>{{tr('Живое состояние процессов, контейнера, устройств и relay-нагрузки.','Live state of processes, container resources, devices, and relay load.')}}</p></div><button class="secondary-button" :disabled="loading" @click="load"><RefreshCw :size="15" :class="{spinning:loading}"/>{{t('refresh')}}</button></section>
  <p v-if="error" class="alert-error">{{error}}</p>
  <section class="metric-grid">
    <MetricCard :label="t('apiServer')" :value="info?.api||'—'" :detail="`${tr('работает','uptime')} ${formatUptime(info?.uptime_seconds)}`" :icon="Server" tone="cyan"/>
    <MetricCard label="HBBS" :value="info?.hbbs||'—'" :detail="`${info?.hbbs_instances??0} ${tr('узлов','nodes')} · ${info?.rendezvous_peers??0} peers`" :icon="RadioTower" tone="green"/>
    <MetricCard label="HBBR" :value="info?.hbbr||'—'" :detail="`${info?.hbbr_instances??0} ${tr('узлов','nodes')} · ${info?.relay_connections??0} ${tr('соединений','connections')}`" :icon="Cable" tone="green"/>
    <MetricCard :label="t('database')" :value="info?.database||'—'" :detail="info?.database_driver||'—'" :icon="Database" tone="violet"/>
    <MetricCard label="CPU" :value="`${(info?.cpu_percent??0).toFixed(1)}%`" :detail="`${info?.cpu_count??0} ${tr('логических CPU','logical CPUs')}`" :icon="Cpu" tone="amber"/>
    <MetricCard label="RAM working set" :value="formatMemory(info?.memory_bytes)" :detail="`cgroup ${tr('с кешем','with cache')} ${formatMemory(info?.memory_cgroup_bytes)}`" :icon="MemoryStick" tone="violet"/>
    <MetricCard label="Relay traffic" :value="formatRate(info?.relay_bandwidth)" :detail="`${info?.healthy_relays??0}/${info?.relay_servers??0} ${tr('relay исправны','relays healthy')}`" :icon="Activity" tone="cyan"/>
    <MetricCard :label="t('managedUsers')" :value="info?.users??'—'" :detail="`${info?.active_sessions??0} ${tr('сессий','sessions')} · ${info?.managed_devices??0} ${tr('устройств','devices')}`" :icon="Users" tone="amber"/>
  </section>
  <section class="panel table-panel"><div class="panel-heading"><div><p class="eyebrow">Production diagnostics</p><h3>{{tr('Самодиагностика сервера','Server self-diagnostics')}}</h3><p>{{tr('Проверка базы, persistent data, прав секретов, HBBS/HBBR и ревизии auth-кеша.','Database, persistent data, secret permissions, HBBS/HBBR and auth-cache revision checks.')}}</p></div><span class="badge" :class="diagnostics?.status==='ok'?'green':diagnostics?.status==='warning'?'amber':'red'">{{diagnostics?.status||'—'}}</span></div><div class="diagnostic-grid"><article v-for="check in diagnostics?.checks||[]" :key="check.name" class="diagnostic-item"><CheckCircle2 v-if="check.status==='ok'" :size="19"/><TriangleAlert v-else-if="check.status==='warning'" :size="19"/><CircleAlert v-else :size="19"/><div><strong>{{check.name}}</strong><small>{{check.message}}</small></div><span class="badge" :class="check.status==='ok'?'green':check.status==='warning'?'amber':'red'">{{check.status}}</span></article></div><div class="diagnostic-summary"><span>Auth cache: <code>{{diagnostics?.auth_cache_source_id||'—'}}</code> · rev {{diagnostics?.auth_cache_revision??0}}</span><span>Trusted proxies: {{diagnostics?.trusted_proxy_count??0}}</span></div></section>
  <section class="telemetry-panel">
    <div class="panel-heading"><div><p class="eyebrow">History</p><h3>{{tr('История нагрузки','Load history')}}</h3></div><span class="muted-text">{{tr('до 30 минут','up to 30 minutes')}} · {{history.length}} {{tr('точек','points')}}</span></div>
    <div v-if="history.length<2" class="empty-state">{{tr('История появится после следующего обновления.','History will appear after the next refresh.')}}</div>
    <div v-else class="telemetry-grid">
      <article v-for="chart in [{label:'CPU',value:`${(info?.cpu_percent??0).toFixed(1)}%`,points:cpuPoints,tone:'cyan'},{label:'RAM',value:formatMemory(info?.memory_bytes),points:memoryPoints,tone:'violet'},{label:'Online devices',value:String(info?.online_devices??0),points:devicePoints,tone:'green'},{label:'Relay connections',value:String(info?.relay_connections??0),points:relayPoints,tone:'amber'}]" :key="chart.label" class="telemetry-chart">
        <div><span>{{chart.label}}</span><strong>{{chart.value}}</strong></div>
        <svg viewBox="0 0 100 44" preserveAspectRatio="none" aria-hidden="true"><path d="M0 42H100" class="chart-axis"/><polyline :points="chart.points" :class="`chart-line ${chart.tone}`"/></svg>
      </article>
    </div>
  </section>
  <section class="panel table-panel"><div class="panel-heading"><div><p class="eyebrow">Server control</p><h3>{{tr('Управление узлами','Node control')}}</h3><p>{{tr('Команды доставляются через аутентифицированный internal channel и подтверждаются каждым HBBS.','Commands are delivered over the authenticated internal channel and acknowledged by each HBBS node.')}}</p></div><button class="secondary-button" :disabled="commandBusy" @click="reconcile"><RotateCw :size="15" :class="{spinning:commandBusy}"/>{{tr('Синхронизировать кеш HBBS','Reconcile HBBS cache')}}</button></div><div class="table-wrap"><table><thead><tr><th>{{tr('Команда','Command')}}</th><th>{{tr('Назначение','Target')}}</th><th>{{tr('Создана','Created')}}</th><th>{{t('status')}}</th></tr></thead><tbody><tr v-for="command in [...commands].reverse().slice(0,20)" :key="command.id"><td>{{command.type}}</td><td>{{command.service}} · {{command.target_instance}}</td><td>{{new Date(command.created_at).toLocaleString()}}</td><td><span class="badge" :class="command.acknowledged_at?'green':'neutral'">{{command.acknowledged_at?`${tr('выполнена','completed')} · ${command.acknowledged_by}`:tr('ожидает','pending')}}</span></td></tr></tbody></table></div></section>
</template>
