<template>
  <div class="flex min-w-0 items-center gap-3">
    <img :src="logo || '/relay-mark.svg'" alt="" class="h-10 w-10 shrink-0 rounded-xl object-contain" />
    <div v-if="!iconOnly" class="min-w-0">
      <span class="block truncate text-base font-bold tracking-tight">{{ name }}</span>
      <span class="block text-[10px] font-medium uppercase tracking-[.2em] opacity-50">Intelligence, connected.</span>
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{ name: string; logo?: string; iconOnly?: boolean }>()
</script>
