<script setup lang="ts">
import { ref } from 'vue'
import { Download, FileArchive, ShieldCheck } from 'lucide-vue-next'
import { api } from '../api'
import { tr } from '../preferences'

const busy = ref(false)
const error = ref('')

async function downloadBundle() {
  busy.value = true
  error.value = ''
  try {
    const result = await api.supportBundle()
    const url = URL.createObjectURL(result.blob)
    const link = document.createElement('a')
    link.href = url
    link.download = result.filename
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : tr('Не удалось создать пакет поддержки', 'Could not create support bundle')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="section-heading"><div><p class="eyebrow">Supportability</p><h2>{{ tr('Поддержка', 'Support') }}</h2><p>{{ tr('Безопасная диагностика установки без паролей, токенов и содержимого соединений.', 'Safe installation diagnostics without passwords, tokens, or connection content.') }}</p></div></section>
  <p v-if="error" class="alert-error">{{ error }}</p>
  <section class="support-grid"><article class="panel support-card">
    <div class="panel-heading"><div><p class="overline">Redacted diagnostics</p><h3>{{ tr('Пакет поддержки', 'Support bundle') }}</h3></div><FileArchive :size="22" /></div>
    <p>{{ tr('ZIP содержит версию, безопасные настройки, состояние кластера, счётчики и последние события аудита с удалёнными персональными данными.', 'The ZIP contains version, safe settings, cluster state, counters, and recent audit events with personal data removed.') }}</p>
    <ul class="support-list"><li><ShieldCheck :size="16" />{{ tr('Секреты и токены не включаются', 'Secrets and tokens are excluded') }}</li><li><ShieldCheck :size="16" />{{ tr('IP, имена пользователей и содержимое файлов не включаются', 'IP addresses, usernames, and file contents are excluded') }}</li><li><ShieldCheck :size="16" />{{ tr('Скачивание записывается в аудит', 'The download is recorded in audit') }}</li></ul>
    <button class="primary-button compact" :disabled="busy" @click="downloadBundle"><Download :size="16" />{{ busy ? tr('Подготовка…', 'Preparing…') : tr('Скачать пакет', 'Download bundle') }}</button>
  </article></section>
</template>

<style scoped>
.support-grid{display:grid;grid-template-columns:minmax(320px,720px);gap:14px}.support-card p{color:var(--muted);font-size:11px;line-height:1.65}.support-list{display:grid;gap:10px;margin:18px 0;padding:0;list-style:none}.support-list li{display:flex;align-items:center;gap:9px;color:var(--text);font-size:11px}.support-list svg{color:var(--success)}
</style>
