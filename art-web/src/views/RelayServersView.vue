<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { Cable, CheckCircle2, Pencil, Plus, Power, Trash2, X } from 'lucide-vue-next'
import { api } from '../api'
import RelayQuarantine from '../components/RelayQuarantine.vue'
import { auth } from '../auth'
import { tr } from '../preferences'
import type { Infrastructure, RelayDeliveryStatus, RelayMetric, RelayServer, RuntimeSettings } from '../types'

const records=ref<RelayServer[]>([]),settings=ref<RuntimeSettings>(),infra=ref<Infrastructure>(),editing=ref<RelayServer>(),open=ref(false),saving=ref(false),error=ref('')
const selected=ref<RelayServer>(),metrics=ref<RelayMetric[]>([])
const delivery=ref<RelayDeliveryStatus>(),deliveryError=ref(''),deliveryLoading=ref(false),compacting=ref(false),compactResult=ref('')
let deliveryRequest=0
const checkedAt=ref(Date.now())
const deliveryFresh=computed(()=>Boolean(delivery.value?.runtime.connected&&delivery.value.runtime.delivery&&checkedAt.value-new Date(delivery.value.runtime.observed_at).getTime()<20000))
function dateLabel(value:number){return value>0?new Date(value).toLocaleString():'—'}
async function loadDelivery(){const id=selected.value?.id;if(!id)return;const request=++deliveryRequest;deliveryLoading.value=true;deliveryError.value='';try{const result=await api.relayDelivery(id);if(selected.value?.id===id&&request===deliveryRequest){delivery.value=result;checkedAt.value=Date.now()}}catch{if(selected.value?.id===id&&request===deliveryRequest){delivery.value=undefined;deliveryError.value=tr('Состояние очереди недоступно. Повторите обновление.','Delivery status unavailable. Please refresh.')}}finally{if(request===deliveryRequest)deliveryLoading.value=false}}
async function compactHistory(){const id=selected.value?.id;if(!id||!delivery.value)return;if(!confirm(tr(`Уплотнить до ${delivery.value.batch_limit} завершённых записей старше ${delivery.value.retention_days} дней? Защита от повторов сохранится.`,`Compact up to ${delivery.value.batch_limit} completed records older than ${delivery.value.retention_days} days? Replay protection is preserved.`)))return;compacting.value=true;compactResult.value='';try{const result=await api.compactRelayDelivery(id);if(selected.value?.id===id)compactResult.value=tr(`Архивировано: ${result.archived}`,`Archived: ${result.archived}`);await loadDelivery()}catch{deliveryError.value=tr('Не удалось уплотнить историю.','Could not compact history.')}finally{compacting.value=false}}
const enrollment=ref<{relay_id:string;enrollment_token:string;expires_at:string}>()
async function enroll(value:RelayServer){
  if(value.control_mode==='wss'&&!confirm(tr('Перевыпустить подключение? Прежний ключ relay будет отозван.','Re-enroll this relay? Its previous credential will be revoked.')))return
  try{enrollment.value=await api.relayEnrollment(value.id);await load()}catch(e){fail(e,tr('Не удалось создать код подключения','Could not create enrollment code'))}
}
async function revoke(value:RelayServer){
  if(!confirm(tr('Отозвать ключ и отключить управление этим relay?','Revoke this relay credential and disconnect control?')))return
  try{await api.revokeRelayCredential(value.id);await load()}catch(e){fail(e,tr('Не удалось отозвать ключ','Could not revoke credential'))}
}
const form=reactive({name:'',hostname:'',port:21117,region:'',enabled:true})
async function load(){error.value='';try{[records.value,settings.value,infra.value]=await Promise.all([api.relayServers(),api.settings(),api.infrastructure()])}catch(e){fail(e,tr('Ошибка загрузки','Load failed'))}}
function add(){editing.value=undefined;Object.assign(form,{name:'',hostname:'',port:21117,region:'',enabled:true});open.value=true}
function edit(value:RelayServer){editing.value=value;Object.assign(form,{name:value.name,hostname:value.hostname,port:value.port,region:value.region,enabled:value.enabled});open.value=true}
async function save(){saving.value=true;try{editing.value?await api.updateRelayServer(editing.value.id,{...form}):await api.createRelayServer({...form});open.value=false;await load()}catch(e){fail(e,tr('Ошибка сохранения','Save failed'))}finally{saving.value=false}}
async function toggle(value:RelayServer){try{await api.updateRelayServer(value.id,{...value,enabled:!value.enabled});await load()}catch(e){fail(e,tr('Ошибка обновления','Update failed'))}}
async function remove(value:RelayServer){if(!confirm(tr(`Удалить relay «${value.name}»?`,`Delete relay “${value.name}”?`)))return;try{await api.deleteRelayServer(value.id);await load()}catch(e){fail(e,tr('Ошибка удаления','Delete failed'))}}
function fail(value:unknown,fallback:string){error.value=value instanceof Error?value.message:fallback}
function formatRate(value:number){if(value<1024)return `${value} B/s`;if(value<1024**2)return `${(value/1024).toFixed(1)} KiB/s`;if(value<1024**3)return `${(value/1024**2).toFixed(1)} MiB/s`;return `${(value/1024**3).toFixed(1)} GiB/s`}
async function showMetrics(value:RelayServer){selected.value=value;delivery.value=undefined;compactResult.value='';void loadDelivery();try{const result=await api.relayMetrics(value.id);if(selected.value?.id===value.id)metrics.value=result}catch(e){fail(e,tr('История relay недоступна','Relay history unavailable'))}}
let refreshTimer:number|undefined
onMounted(()=>{void load();refreshTimer=window.setInterval(()=>{checkedAt.value=Date.now();void load();void loadDelivery()},15000)})
onUnmounted(()=>{if(refreshTimer)window.clearInterval(refreshTimer)})
</script>

<template>
  <section class="section-heading">
    <div><p class="eyebrow">Relay topology</p><h2>{{tr('Relay-серверы','Relay servers')}}</h2><p>{{tr('Активный мониторинг HBBR-узлов, регионов, нагрузки и задержки.','Live monitoring of HBBR nodes, regions, load, and latency.')}}</p></div>
    <button class="primary-button compact" @click="add"><Plus :size="17"/>{{tr('Добавить relay','Add relay')}}</button>
  </section>
  <p v-if="error" class="alert-error">{{error}}</p>
  <section v-if="enrollment" class="panel"><div class="panel-heading"><h3>{{tr('Одноразовое подключение relay','One-time relay enrollment')}}</h3><button class="icon-btn" @click="enrollment=undefined"><X :size="20"/></button></div><p>{{tr('Код показывается только сейчас. Он действует до','This code is shown only now. It expires at')}} {{new Date(enrollment.expires_at).toLocaleString()}}.</p><label><span>Relay ID</span><input readonly :value="enrollment.relay_id"/></label><label><span>{{tr('Код подключения','Enrollment code')}}</span><input readonly :value="enrollment.enrollment_token" autocomplete="off"/></label></section>
  <section class="card-grid">
    <article v-for="value in records" :key="value.id" class="panel group-card policy-card" :class="{inactive:!value.enabled}" @click="showMetrics(value)">
      <span class="metric-icon"><Cable :size="20"/></span>
      <button class="secondary-button compact" @click.stop="showMetrics(value)">{{tr('Очередь и история','Queue and history')}}</button>
      <div class="policy-content"><div><span class="badge" :class="value.health==='healthy'?'green':'neutral'">{{value.enabled?value.health:tr('отключён','disabled')}}</span><span class="badge neutral">{{value.region||tr('по умолчанию','default')}}</span><span v-if="value.latency_ms" class="badge teal">{{value.latency_ms}} ms</span><span class="badge neutral">{{value.connections}} {{tr('соединений','connections')}}</span><span class="badge neutral">{{formatRate(value.bandwidth)}}</span></div><h3>{{value.name}}</h3><p>{{value.hostname}}:{{value.port}} · {{tr('обновлено','updated')}} {{new Date(value.updated_at).toLocaleTimeString()}}</p></div>
      <div><span class="badge neutral">{{value.control_mode==='wss'?(value.control_connected?tr('Управление подключено','Control connected'):tr('Управление отключено','Control offline')):'Legacy UDP'}}</span><button v-if="value.enabled" class="secondary-button compact" @click.stop="enroll(value)">{{tr('Подключить / сменить ключ','Enroll / rotate key')}}</button><button v-if="value.control_mode==='wss'" class="secondary-button compact" @click.stop="revoke(value)">{{tr('Отозвать ключ','Revoke key')}}</button></div>
      <div class="policy-actions"><button class="icon-btn" :title="tr('Редактировать','Edit')" @click.stop="edit(value)"><Pencil :size="15"/></button><button class="icon-btn" :title="value.enabled?tr('Отключить','Disable'):tr('Включить','Enable')" @click.stop="toggle(value)"><Power :size="15"/></button><button class="icon-action" :title="tr('Удалить','Delete')" @click.stop="remove(value)"><Trash2 :size="15"/></button></div>
    </article>
    <article v-if="!records.length" class="panel group-card"><span class="metric-icon green"><Cable :size="20"/></span><div><span class="badge green"><CheckCircle2 :size="12"/>{{infra?.hbbr||tr('загрузка','loading')}}</span><h3>{{tr('Встроенный HBBR','Built-in HBBR')}}</h3><p>{{settings?.relay_server||tr('Не настроен','Not configured')}} · {{tr('ожидание телеметрии','waiting for telemetry')}}</p></div></article>
  </section>
  <section v-if="selected" class="panel table-panel">
    <RelayQuarantine :key="selected.id" :relay-id="selected.id" @changed="loadDelivery" />
    <div class="panel-heading"><h3>{{selected.name}} · {{tr('Доставка событий','Event delivery')}}</h3><button class="secondary-button" :disabled="deliveryLoading" @click="loadDelivery">{{tr('Обновить','Refresh')}}</button></div>
    <p v-if="deliveryError" class="alert-error" role="alert">{{deliveryError}}</p>
    <p v-else-if="!delivery">{{tr('Загрузка…','Loading…')}}</p>
    <template v-else>
      <p v-if="!deliveryFresh" class="muted-text">{{tr('Свежая телеметрия очереди недоступна: relay отключён, данные устарели или требуется обновление узла.','Fresh queue telemetry unavailable: relay is offline, data is stale, or the node needs upgrading.')}}</p>
      <div v-if="deliveryFresh&&delivery.runtime.delivery" class="delivery-summary">
        <span>{{tr('Ожидают отправки','Pending')}}: <strong>{{delivery.runtime.delivery.pending}}</strong></span><span>{{tr('Карантин','Quarantine')}}: <strong>{{delivery.runtime.delivery.quarantined}}</strong></span><span>{{tr('Резерв закрытия','Close reservations')}}: {{delivery.runtime.delivery.reserved}}</span><span>{{tr('Общий лимит','Total limit')}}: {{delivery.runtime.delivery.capacity}}</span>
        <span class="badge" :class="delivery.runtime.delivery.healthy?'green':'danger'">{{delivery.runtime.delivery.healthy?tr('Хранилище доступно','Storage available'):tr('Ошибка записи на диск','Storage write error')}}</span>
        <span v-if="delivery.runtime.delivery.blocked" class="badge amber">{{tr('Новые пары заблокированы','New pairs blocked')}}</span>
        <span>{{tr('Старейшее ожидающее событие','Oldest pending event')}}: {{dateLabel(delivery.runtime.delivery.oldest_at)}}</span>
      </div>
      <p>{{tr('История API: разрешения','API history: authorizations')}} {{delivery.history.authorizations}} · {{tr('подтверждения','receipts')}} {{delivery.history.receipts}} · {{tr('компактный архив','compact archive')}} {{delivery.history.archived}}</p>
      <p class="muted-text">{{tr('Уплотняются только завершённые записи старше','Only completed records older than')}} {{delivery.retention_days}} {{tr('дней. Поздние повторы остаются защищены; активные записи и очередь relay не удаляются.','days are compacted. Late repeats remain protected; active records and the relay queue are retained.')}}</p>
      <button v-if="auth.can('relays.write')" class="secondary-button" :disabled="compacting||!delivery.history.eligible" @click="compactHistory">{{tr('Уплотнить историю','Compact history')}} ({{delivery.history.eligible}})</button><p v-if="compactResult" role="status">{{compactResult}}</p>
      <template v-if="deliveryFresh&&delivery.runtime.delivery?.quarantined">
        <h4>{{tr('Карантин: первые 5 событий','Quarantine: first 5 events')}}</h4><p class="muted-text">{{tr('События прежнего ключа сохранены на томе relay и не применяются к соединениям. Полный список и экспорт доступны в блоке обслуживания карантина; автоматического удаления нет.','Events from an earlier credential remain on the relay volume and do not change connections. Use quarantine maintenance for the full list and export; no automatic deletion.')}}</p>
        <div class="table-wrap"><table><thead><tr><th>{{tr('Событие','Event')}}</th><th>UUID</th><th>{{tr('Состояние','State')}}</th><th>{{tr('Причина','Reason')}}</th></tr></thead><tbody><tr v-for="event in delivery.runtime.delivery.sample" :key="event.id"><td>{{event.id}}</td><td>{{event.uuid}}</td><td>{{event.status}}</td><td>{{tr('Смена поколения ключа','Credential generation changed')}}</td></tr></tbody></table></div>
      </template>
    </template>
  </section>
  <section v-if="selected" class="panel table-panel"><div class="panel-heading"><h3>{{tr('Метрики за 24 часа','Metrics over 24 hours')}}</h3></div><div class="table-wrap"><table><thead><tr><th>{{tr('Время','Time')}}</th><th>{{tr('Состояние','Health')}}</th><th>{{tr('Задержка','Latency')}}</th><th>{{tr('Соединения','Connections')}}</th><th>{{tr('Трафик','Traffic')}}</th></tr></thead><tbody><tr v-for="item in metrics" :key="item.recorded_at"><td>{{new Date(item.recorded_at).toLocaleString()}}</td><td>{{item.health}}</td><td>{{item.latency_ms}} ms</td><td>{{item.connections}}</td><td>{{formatRate(item.bandwidth)}}</td></tr></tbody></table></div></section>
  <div v-if="open" class="modal-backdrop" @click.self="open=false"><form class="modal-card" @submit.prevent="save"><div class="modal-heading"><h3>{{editing?tr('Редактирование relay','Edit relay'):tr('Новый relay-сервер','New relay server')}}</h3><button type="button" class="icon-btn" @click="open=false"><X :size="20"/></button></div><div class="form-grid"><label><span>{{tr('Название','Name')}}</span><input v-model="form.name" required minlength="2" maxlength="128"/></label><label><span>{{tr('Регион','Region')}}</span><input v-model="form.region" maxlength="64" placeholder="msk-1"/></label><label><span>Hostname / IP</span><input v-model="form.hostname" required maxlength="253"/></label><label><span>{{tr('Порт','Port')}}</span><input v-model.number="form.port" type="number" min="1" max="65535" required/></label><label v-if="editing" class="check-label"><input v-model="form.enabled" type="checkbox"/><span>{{tr('Relay включён','Relay enabled')}}</span></label></div><div class="modal-actions"><button type="button" class="secondary-button" @click="open=false">{{tr('Отмена','Cancel')}}</button><button class="primary-button compact" :disabled="saving">{{saving?tr('Сохранение…','Saving…'):tr('Сохранить','Save')}}</button></div></form></div>
</template>
<style scoped>.delivery-summary{display:flex;gap:12px;flex-wrap:wrap;padding:12px 0}.table-panel>p,.table-panel>h4,.delivery-summary{margin-left:20px;margin-right:20px}.table-panel>.secondary-button{margin:0 20px 16px}</style>
