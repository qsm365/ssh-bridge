<script setup lang="ts">
import { locale, setLocale, t, type Locale } from '../i18n'

function change(event: Event) {
  setLocale((event.target as HTMLSelectElement).value as Locale)
}
</script>

<template>
  <label class="language-select">
    <svg class="language-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.4 2.5 3.6 5.5 3.6 9s-1.2 6.5-3.6 9c-2.4-2.5-3.6-5.5-3.6-9S9.6 5.5 12 3Z" />
    </svg>
    <select :value="locale" @change="change" :aria-label="t('语言')">
      <option value="zh-CN">中文</option>
      <option value="en">English</option>
    </select>
  </label>
</template>
