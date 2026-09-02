<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

const source = ref('/api/branding/logo')
const fallback = '/remote-control-server-logo.svg'
function useFallback() { source.value = fallback }
function refresh() { source.value = `/api/branding/logo?v=${Date.now()}` }
onMounted(() => window.addEventListener('rds-branding-changed', refresh))
onBeforeUnmount(() => window.removeEventListener('rds-branding-changed', refresh))
</script>

<template>
  <span class="brand-mark" aria-hidden="true">
    <img :src="source" alt="" @error="useFallback" />
  </span>
</template>

<style scoped>
.brand-mark {
  display:grid;
  place-items:center;
  width:42px;
  height:42px;
  overflow:visible;
  flex:0 0 auto;
  border:0;
  background:transparent;
  filter:drop-shadow(0 10px 18px rgba(20,94,255,.28));
}
.brand-mark img { width:100%; height:100%; object-fit:contain; }
</style>
