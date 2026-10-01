<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'

const route = useRoute()
const router = useRouter()
const password = ref('')
const error = ref('')
const loading = ref(false)

async function login() {
  error.value = ''
  loading.value = true
  try {
    await api.login(password.value)
    password.value = ''
    const requested = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    await router.replace(requested.startsWith('/') && !requested.startsWith('//') ? requested : '/sessions')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-decoration"><span></span><span></span><span></span></div>
    <div class="login-card">
      <div class="login-logo">S</div>
      <h1>登录 SSH Bridge</h1>
      <p>Server 模式管理员入口</p>
      <form @submit.prevent="login">
        <label><span>用户名</span><input value="admin" autocomplete="username" readonly /></label>
        <label><span>管理员密码</span><input v-model="password" type="password" autocomplete="current-password" required autofocus /></label>
        <p v-if="error" class="form-error">{{ error }}</p>
        <button class="primary-button login-button" :disabled="loading">{{ loading ? '正在登录…' : '登录' }}</button>
      </form>
      <small>登录会话仅保存在当前服务进程中</small>
    </div>
  </div>
</template>
