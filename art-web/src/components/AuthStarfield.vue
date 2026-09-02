<script setup lang="ts">
type Star = {
  id: number
  x: number
  y: number
  size: number
  opacity: number
  delay: number
  duration: number
  flare: boolean
}

const stars: Star[] = Array.from({ length: 175 }, (_, id) => ({
  id,
  x: Math.random() * 100,
  y: Math.random() * 100,
  size: Math.random() < 0.12 ? 2 + Math.random() * 1.5 : 0.7 + Math.random() * 1.4,
  opacity: 0.32 + Math.random() * 0.68,
  delay: -(Math.random() * 8),
  duration: 2.4 + Math.random() * 5.6,
  flare: Math.random() < 0.13,
}))

</script>

<template>
  <div class="auth-starfield" aria-hidden="true">
    <span class="auth-nebula auth-nebula-one"></span>
    <span class="auth-nebula auth-nebula-two"></span>
    <span
      v-for="star in stars"
      :key="star.id"
      class="auth-star"
      :class="{ 'auth-star-flare': star.flare }"
      :style="{
        left: `${star.x}%`,
        top: `${star.y}%`,
        width: `${star.size}px`,
        height: `${star.size}px`,
        opacity: star.opacity,
        animationDelay: `${star.delay}s`,
        animationDuration: `${star.duration}s`,
      }"
    ></span>
  </div>
</template>
