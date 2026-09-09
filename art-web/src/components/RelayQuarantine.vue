<script setup lang="ts">
import { ref, onUnmounted } from 'vue'
import { api } from '../api'
import { auth } from '../auth'
import { tr } from '../preferences'
const props=defineProps<{relayId:string}>()
const emit=defineEmits<{changed:[]}>()
const page=ref<Awaited<ReturnType<typeof api.relayQuarantine>>>()
const busy=ref(false),error=ref(''),result=ref(''),exported=ref('')
let mounted=true
onUnmounted(()=>{mounted=false})
async function load(offset=0,refresh=false){
 busy.value=true;error.value=''
 if(refresh){exported.value='';page.value=undefined}
 try{const value=await api.relayQuarantine(props.relayId,refresh?'':page.value?.revision,offset);if(mounted)page.value=value}
 catch{error.value=tr('Карантин изменился или relay недоступен. Обновите список.','Quarantine changed or relay unavailable. Refresh the list.');exported.value=''}
 finally{busy.value=false}
}
async function download(){
 if(!page.value)return
 busy.value=true;error.value=''
 try{
  const value=await api.exportRelayQuarantine(props.relayId,page.value.revision)
  if(!mounted)return
  const url=URL.createObjectURL(new Blob([JSON.stringify(value,null,2)],{type:'application/json'}))
  const a=document.createElement('a');a.href=url;a.download='relay-quarantine.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)
  exported.value=value.revision
 }catch{error.value=tr('Экспорт не завершён. Обновите список и повторите.','Export failed. Refresh and retry.');exported.value=''}finally{busy.value=false}
}
async function archive(){
 const revision=page.value?.revision
 if(!revision||exported.value!==revision)return
 if(!confirm(tr('Убедитесь, что экспорт сохранён. Перенести этот карантин в резервный файл на relay и освободить очередь? События не будут повторно применены.','Ensure the export was saved. Archive this quarantine on the relay and free queue capacity? Events will not be reapplied.')))return
 busy.value=true;error.value='';result.value=''
 try{const value=await api.archiveRelayQuarantine(props.relayId,revision);if(!mounted)return;result.value=tr('Сохранена резервная копия: ','Backup saved: ')+value.backup;exported.value='';await load(0,true);emit('changed')}
 catch{error.value=tr('Операция не подтверждена. Обновите список: карантин мог измениться или ответ был потерян.','Operation not confirmed. Refresh: quarantine may have changed or the reply was lost.');exported.value=''}
 finally{busy.value=false}
}
</script>
<template>
 <div class="quarantine-panel">
  <h3>{{tr('Обслуживание карантина','Quarantine maintenance')}}</h3>
  <p>{{tr('События прежнего ключа можно выгрузить и перенести в резервный файл. Активная очередь не затрагивается.','Export events from previous credentials and archive them. The active delivery queue is preserved.')}}</p>
  <div class="actions">
   <button class="secondary-button" :disabled="busy" @click="load(0,true)">{{tr('Открыть / обновить список','Open / refresh list')}}</button>
   <template v-if="page&&auth.can('relays.write')">
    <button class="secondary-button" :disabled="busy||!page.total" @click="download">{{tr('Скачать полный JSON','Download full JSON')}}</button>
    <button class="secondary-button" :disabled="busy||!page.total||exported!==page.revision" @click="archive">{{tr('Архивировать и освободить очередь','Archive and free queue')}}</button>
   </template>
  </div>
  <p v-if="busy" role="status">{{tr('Выполняется…','Working…')}}</p>
  <p v-if="error" class="alert-error" role="alert">{{error}}</p><p v-if="result" role="status">{{result}}</p>
  <template v-if="page">
   <p>{{tr('Всего событий','Total events')}}: {{page.total}}</p>
   <div class="table-wrap"><table><thead><tr><th>{{tr('Событие','Event')}}</th><th>UUID</th><th>{{tr('Состояние','State')}}</th><th>{{tr('Время','Time')}}</th></tr></thead><tbody><tr v-for="event in page.events" :key="event.id"><td>{{event.id}}</td><td>{{event.uuid}}</td><td>{{event.status}}</td><td>{{event.created_at?new Date(event.created_at).toLocaleString():'—'}}</td></tr></tbody></table></div>
   <div class="actions"><button class="secondary-button" :disabled="busy||page.offset===0" @click="load(Math.max(0,page.offset-10))">{{tr('Назад','Previous')}}</button><span>{{page.total?page.offset+1:0}}–{{page.offset+page.events.length}} / {{page.total}}</span><button class="secondary-button" :disabled="busy||page.offset+page.events.length>=page.total" @click="load(page.offset+10)">{{tr('Далее','Next')}}</button></div>
  </template>
 </div>
</template>
<style scoped>.quarantine-panel{padding:20px}.actions{display:flex;flex-wrap:wrap;gap:12px;align-items:center;margin:12px 0}td{overflow-wrap:anywhere}</style>
