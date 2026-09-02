<script setup lang="ts">
import { ref } from 'vue'
import { Container, Download, ExternalLink, FileArchive, Github, HeartHandshake, ShieldCheck } from 'lucide-vue-next'
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
  <section class="section-heading"><div><p class="eyebrow">Supportability</p><h2>{{ tr('Поддержка', 'Support') }}</h2><p>{{ tr('Диагностика установки, документация и добровольная поддержка развития проекта.', 'Installation diagnostics, documentation, and voluntary project support.') }}</p></div></section>
  <p v-if="error" class="alert-error">{{ error }}</p>
  <section class="support-grid"><article class="panel support-card">
    <div class="panel-heading"><div><p class="overline">Redacted diagnostics</p><h3>{{ tr('Пакет поддержки', 'Support bundle') }}</h3></div><FileArchive :size="22" /></div>
    <p>{{ tr('ZIP содержит версию, безопасные настройки, состояние кластера, счётчики и последние события аудита с удалёнными персональными данными.', 'The ZIP contains version, safe settings, cluster state, counters, and recent audit events with personal data removed.') }}</p>
    <ul class="support-list"><li><ShieldCheck :size="16" />{{ tr('Секреты и токены не включаются', 'Secrets and tokens are excluded') }}</li><li><ShieldCheck :size="16" />{{ tr('IP, имена пользователей и содержимое файлов не включаются', 'IP addresses, usernames, and file contents are excluded') }}</li><li><ShieldCheck :size="16" />{{ tr('Скачивание записывается в аудит', 'The download is recorded in audit') }}</li></ul>
    <button class="primary-button compact" :disabled="busy" @click="downloadBundle"><Download :size="16" />{{ busy ? tr('Подготовка…', 'Preparing…') : tr('Скачать пакет', 'Download bundle') }}</button>
  </article>
  <article class="panel support-card project-support-card">
    <div class="panel-heading"><div><p class="overline">Open-source development</p><h3>{{ tr('Поддержать проект', 'Support the project') }}</h3></div><HeartHandshake :size="22" /></div>
    <p>{{ tr('Remote Control Server остаётся бесплатным. Поддержка помогает тестировать новые версии RouterOS и клиентов, сохранять совместимость протокола и развивать функции безопасности и управления.', 'Remote Control Server remains free. Support helps test new RouterOS and client releases, maintain protocol compatibility, and develop security and management features.') }}</p>
    <div class="project-links">
      <a class="primary-button compact" href="https://boosty.to/blackxdog/donate" target="_blank" rel="noopener noreferrer"><HeartHandshake :size="16" />{{ tr('Поддержать на Boosty', 'Support on Boosty') }}<ExternalLink :size="14" /></a>
      <a class="secondary-button compact" href="https://github.com/blackxd0g/remote-control-server-routeros" target="_blank" rel="noopener noreferrer"><Github :size="16" />GitHub<ExternalLink :size="14" /></a>
      <a class="secondary-button compact" href="https://hub.docker.com/r/blackxdog/remote-control-server-routeros" target="_blank" rel="noopener noreferrer"><Container :size="16" />Docker Hub<ExternalLink :size="14" /></a>
    </div>
    <small>{{ tr('Подписка и разовый донат полностью добровольны и не ограничивают доступ к основным возможностям проекта.', 'Subscriptions and one-time donations are entirely voluntary and do not restrict access to the project’s core features.') }}</small>
  </article></section>
</template>

<style scoped>
.support-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.support-card{min-width:0}.support-card p{color:var(--muted);font-size:11px;line-height:1.65}.support-list{display:grid;gap:10px;margin:18px 0;padding:0;list-style:none}.support-list li{display:flex;align-items:center;gap:9px;color:var(--text);font-size:11px}.support-list svg{color:var(--success)}.project-support-card .panel-heading>svg{color:#f15f2c}.project-links{display:flex;flex-wrap:wrap;gap:9px;margin:18px 0}.project-links a{text-decoration:none}.project-links a svg:last-child{opacity:.7}.project-support-card small{display:block;color:var(--muted);font-size:10px;line-height:1.55}@media(max-width:900px){.support-grid{grid-template-columns:1fr}}
</style>
