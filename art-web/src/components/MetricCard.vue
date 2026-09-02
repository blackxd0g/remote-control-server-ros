<script setup lang="ts">
import type { Component } from 'vue'
defineProps<{ label: string; value: string | number; detail: string; icon: Component; tone?: string }>()
</script>

<template>
  <article class="metric-card">
    <div class="metric-head"><span class="metric-icon" :class="tone"><component :is="icon" :size="20" /></span><span class="live-dot"></span></div>
    <strong>{{ value }}</strong><h3>{{ label }}</h3><p>{{ detail }}</p>
  </article>
</template>
