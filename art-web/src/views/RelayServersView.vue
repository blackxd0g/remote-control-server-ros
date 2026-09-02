<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { Cable, CheckCircle2, Pencil, Plus, Power, Trash2, X } from 'lucide-vue-next'
import { api } from '../api'
import { tr } from '../preferences'
import type { Infrastructure, RelayMetric, RelayServer, RuntimeSettings } from '../types'

const records=ref<RelayServer[]>([]),settings=ref<RuntimeSettings>(),infra=ref<Infrastructure>(),editing=ref<RelayServer>(),open=ref(false),saving=ref(false),error=ref('')
const selected=ref<RelayServer>(),metrics=ref<RelayMetric[]>([])
const form=reactive({name:'',hostname:'',port:21117,region:'',enabled:true})
async function load(){error.value='';try{[records.value,settings.value,infra.value]=await Promise.all([api.relayServers(),api.settings(),api.infrastructure()])}catch(e){fail(e,tr('Ошибка загрузки','Load failed'))}}
function add(){editing.value=undefined;Object.assign(form,{name:'',hostname:'',port:21117,region:'',enabled:true});open.value=true}
function edit(value:RelayServer){editing.value=value;Object.assign(form,{name:value.name,hostname:value.hostname,port:value.port,region:value.region,enabled:value.enabled});open.value=true}
async function save(){saving.value=true;try{editing.value?await api.updateRelayServer(editing.value.id,{...form}):await api.createRelayServer({...form});open.value=false;await load()}catch(e){fail(e,tr('Ошибка сохранения','Save failed'))}finally{saving.value=false}}
async function toggle(value:RelayServer){try{await api.updateRelayServer(value.id,{...value,enabled:!value.enabled});await load()}catch(e){fail(e,tr('Ошибка обновления','Update failed'))}}
async function remove(value:RelayServer){if(!confirm(tr(`Удалить relay «${value.name}»?`,`Delete relay “${value.name}”?`)))return;try{await api.deleteRelayServer(value.id);await load()}catch(e){fail(e,tr('Ошибка удаления','Delete failed'))}}
function fail(value:unknown,fallback:string){error.value=value instanceof Error?value.message:fallback}
function formatRate(value:number){if(value<1024)return `${value} B/s`;if(value<1024**2)return `${(value/1024).toFixed(1)} KiB/s`;if(value<1024**3)return `${(value/1024**2).toFixed(1)} MiB/s`;return `${(value/1024**3).toFixed(1)} GiB/s`}
async function showMetrics(value:RelayServer){selected.value=value;try{metrics.value=await api.relayMetrics(value.id)}catch(e){fail(e,tr('История relay недоступна','Relay history unavailable'))}}
let refreshTimer:number|undefined
onMounted(()=>{void load();refreshTimer=window.setInterval(()=>void load(),15000)})
onUnmounted(()=>{if(refreshTimer)window.clearInterval(refreshTimer)})
</script>

<template>
  <section class="section-heading">
    <div><p class="eyebrow">Relay topology</p><h2>{{tr('Relay-серверы','Relay servers')}}</h2><p>{{tr('Активный мониторинг HBBR-узлов, регионов, нагрузки и задержки.','Live monitoring of HBBR nodes, regions, load, and latency.')}}</p></div>
    <button class="primary-button compact" @click="add"><Plus :size="17"/>{{tr('Добавить relay','Add relay')}}</button>
  </section>
  <p v-if="error" class="alert-error">{{error}}</p>
  <section class="card-grid">
    <article v-for="value in records" :key="value.id" class="panel group-card policy-card" :class="{inactive:!value.enabled}" @click="showMetrics(value)">
      <span class="metric-icon"><Cable :size="20"/></span>
      <div class="policy-content"><div><span class="badge" :class="value.health==='healthy'?'green':'neutral'">{{value.enabled?value.health:tr('отключён','disabled')}}</span><span class="badge neutral">{{value.region||tr('по умолчанию','default')}}</span><span v-if="value.latency_ms" class="badge teal">{{value.latency_ms}} ms</span><span class="badge neutral">{{value.connections}} {{tr('соединений','connections')}}</span><span class="badge neutral">{{formatRate(value.bandwidth)}}</span></div><h3>{{value.name}}</h3><p>{{value.hostname}}:{{value.port}} · {{tr('обновлено','updated')}} {{new Date(value.updated_at).toLocaleTimeString()}}</p></div>
      <div class="policy-actions"><button class="icon-btn" :title="tr('Редактировать','Edit')" @click.stop="edit(value)"><Pencil :size="15"/></button><button class="icon-btn" :title="value.enabled?tr('Отключить','Disable'):tr('Включить','Enable')" @click.stop="toggle(value)"><Power :size="15"/></button><button class="icon-action" :title="tr('Удалить','Delete')" @click.stop="remove(value)"><Trash2 :size="15"/></button></div>
    </article>
    <article v-if="!records.length" class="panel group-card"><span class="metric-icon green"><Cable :size="20"/></span><div><span class="badge green"><CheckCircle2 :size="12"/>{{infra?.hbbr||tr('загрузка','loading')}}</span><h3>{{tr('Встроенный HBBR','Built-in HBBR')}}</h3><p>{{settings?.relay_server||tr('Не настроен','Not configured')}} · {{tr('ожидание телеметрии','waiting for telemetry')}}</p></div></article>
  </section>
  <section v-if="selected" class="panel table-panel"><div class="panel-heading"><div><p class="overline">24 hours history</p><h3>{{selected.name}}</h3></div></div><div class="table-wrap"><table><thead><tr><th>{{tr('Время','Time')}}</th><th>{{tr('Состояние','Health')}}</th><th>{{tr('Задержка','Latency')}}</th><th>{{tr('Соединения','Connections')}}</th><th>{{tr('Трафик','Traffic')}}</th></tr></thead><tbody><tr v-for="item in metrics" :key="item.recorded_at"><td>{{new Date(item.recorded_at).toLocaleString()}}</td><td><span class="badge" :class="item.health==='healthy'?'green':'neutral'">{{item.health}}</span></td><td>{{item.latency_ms}} ms</td><td>{{item.connections}}</td><td>{{formatRate(item.bandwidth)}}</td></tr><tr v-if="!metrics.length"><td colspan="5" class="empty-cell">{{tr('История ещё не накоплена.','No history has been collected yet.')}}</td></tr></tbody></table></div></section>
  <div v-if="open" class="modal-backdrop" @click.self="open=false"><form class="modal-card" @submit.prevent="save"><div class="modal-heading"><h3>{{editing?tr('Редактирование relay','Edit relay'):tr('Новый relay-сервер','New relay server')}}</h3><button type="button" class="icon-btn" @click="open=false"><X :size="20"/></button></div><div class="form-grid"><label><span>{{tr('Название','Name')}}</span><input v-model="form.name" required minlength="2" maxlength="128"/></label><label><span>{{tr('Регион','Region')}}</span><input v-model="form.region" maxlength="64" placeholder="msk-1"/></label><label><span>Hostname / IP</span><input v-model="form.hostname" required maxlength="253"/></label><label><span>{{tr('Порт','Port')}}</span><input v-model.number="form.port" type="number" min="1" max="65535" required/></label><label v-if="editing" class="check-label"><input v-model="form.enabled" type="checkbox"/><span>{{tr('Relay включён','Relay enabled')}}</span></label></div><div class="modal-actions"><button type="button" class="secondary-button" @click="open=false">{{tr('Отмена','Cancel')}}</button><button class="primary-button compact" :disabled="saving">{{saving?tr('Сохранение…','Saving…'):tr('Сохранить','Save')}}</button></div></form></div>
</template>
