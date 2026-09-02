<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { auth } from './auth'

onMounted(() => auth.restore())
</script>

<template>
  <RouterView />
</template>
