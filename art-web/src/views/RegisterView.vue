<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight, Globe2, LockKeyhole, MoonStar, ShieldCheck, UserPlus, UserRound } from 'lucide-vue-next'
import AuthStarfield from '../components/AuthStarfield.vue'
import BrandMark from '../components/BrandMark.vue'
import { api } from '../api'
import { preferences, tr, type Locale, type Theme } from '../preferences'
import { appVersion } from '../version'

const form = reactive({ username: '', display_name: '', email: '', password: '', confirmation: '' })
const busy = ref(false)
const error = ref('')
const success = ref('')
const approvalRequired = ref(true)

async function submit() {
  error.value = ''
  success.value = ''
  if (form.password !== form.confirmation) {
    error.value = tr('Пароли не совпадают.', 'Passwords do not match.')
    return
  }
  busy.value = true
  try {
    const result = await api.register({
      username: form.username.trim().toLowerCase(),
      display_name: form.display_name.trim(),
      email: form.email.trim(),
      password: form.password,
    })
    success.value = result.message
    Object.assign(form, { username: '', display_name: '', email: '', password: '', confirmation: '' })
  } catch (reason) {
    error.value = reason instanceof Error
      ? reason.message
      : tr('Не удалось зарегистрировать учётную запись', 'Could not register the account')
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  try {
    approvalRequired.value = (await api.registrationOptions()).approval_required
  } catch {
    approvalRequired.value = true
  }
})
</script>

<template>
  <main class="login-page register-page">
    <AuthStarfield />
    <section class="login-context">
      <div class="login-brand">
        <BrandMark />
        <div><strong>Remote Control Server</strong><span>RouterOS</span></div>
      </div>
      <div class="login-message">
        <span class="eyebrow"><ShieldCheck :size="15" /> {{ tr('Контролируемая регистрация', 'Controlled onboarding') }}</span>
        <h1>
          {{ approvalRequired
            ? tr('Доступ начинается с подтверждённой учётной записи.', 'Access starts with an approved account.')
            : tr('Создайте учётную запись и начните работу.', 'Create your account and get started.') }}
        </h1>
        <p>
          {{ approvalRequired
            ? tr('После регистрации администратор проверит заявку. До одобрения HBBS не разрешит удалённые подключения.', 'An administrator reviews every registration. HBBS blocks remote connections until approval.')
            : tr('Новые учётные записи автоматически авторизуются. Доступ к устройствам по-прежнему ограничивается ACL.', 'New accounts are approved automatically. Device access remains restricted by ACL.') }}
        </p>
        <ul>
          <li>
            <span><UserPlus :size="18" /></span>
            <div><strong>{{ tr('Отдельная учётная запись', 'Personal account') }}</strong><small>{{ tr('Ваши сессии и действия будут привязаны к вашему профилю.', 'Your sessions and actions are linked to your profile.') }}</small></div>
          </li>
          <li>
            <span><ShieldCheck :size="18" /></span>
            <div><strong>{{ tr('Доступ по политике', 'Policy-based access') }}</strong><small>{{ tr('Разрешения на подключения выдаются администратором.', 'Connection permissions are granted by an administrator.') }}</small></div>
          </li>
        </ul>
      </div>
      <small class="login-version">Remote Control Server · v{{appVersion}} · Compatible with the RustDesk client protocol · <a href="https://boosty.to/blackxdog" target="_blank" rel="noopener noreferrer">created by blackxdog</a></small>
    </section>

    <section class="login-panel">
      <form class="login-card register-card" @submit.prevent="submit">
        <div class="login-preferences">
          <label><MoonStar :size="15" /><select :value="preferences.state.theme" @change="preferences.setTheme(($event.target as HTMLSelectElement).value as Theme)"><option value="system">System</option><option value="dark">Dark</option><option value="light">Light</option></select></label>
          <label><Globe2 :size="15" /><select :value="preferences.state.locale" @change="preferences.setLocale(($event.target as HTMLSelectElement).value as Locale)"><option value="ru">RU</option><option value="en">EN</option></select></label>
        </div>
        <div class="mobile-brand"><BrandMark /><strong>Remote Control Server</strong></div>
        <nav class="auth-portal-nav" :aria-label="tr('Выбор способа входа', 'Choose sign-in portal')">
          <RouterLink to="/admin/login"><ShieldCheck :size="15" /><span>{{ tr('Администратор', 'Administrator') }}</span></RouterLink>
          <RouterLink to="/account/login"><UserRound :size="15" /><span>{{ tr('Пользователь', 'User') }}</span></RouterLink>
          <RouterLink class="active" to="/register"><UserPlus :size="15" /><span>{{ tr('Регистрация', 'Register') }}</span></RouterLink>
        </nav>

        <p class="overline">{{ tr('Регистрация пользователя', 'User registration') }}</p>
        <h2>{{ tr('Создать учётную запись', 'Create an account') }}</h2>
        <p class="form-intro">
          {{ approvalRequired
            ? tr('Новая заявка будет отправлена администратору.', 'The new registration will be sent to an administrator.')
            : tr('Учётная запись будет авторизована автоматически.', 'The account will be approved automatically.') }}
        </p>

        <div class="register-form-grid">
          <label><span>{{ tr('Имя пользователя', 'Username') }}</span><div class="input-wrap"><UserRound :size="18" /><input v-model="form.username" required minlength="2" maxlength="64" autocomplete="username" /></div></label>
          <label><span>{{ tr('Отображаемое имя', 'Display name') }}</span><div class="input-wrap"><UserRound :size="18" /><input v-model="form.display_name" maxlength="256" autocomplete="name" /></div></label>
          <label class="register-wide"><span>Email</span><div class="input-wrap"><Globe2 :size="18" /><input v-model="form.email" type="email" autocomplete="email" /></div></label>
          <label><span>{{ tr('Пароль', 'Password') }}</span><div class="input-wrap"><LockKeyhole :size="18" /><input v-model="form.password" type="password" required maxlength="1024" autocomplete="new-password" /></div></label>
          <label><span>{{ tr('Повторите пароль', 'Confirm password') }}</span><div class="input-wrap"><LockKeyhole :size="18" /><input v-model="form.confirmation" type="password" required maxlength="1024" autocomplete="new-password" /></div></label>
        </div>

        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        <p v-if="success" class="alert-success" role="status">{{ success }}</p>
        <button class="primary-button" :disabled="busy || !!success">
          <span>{{ busy ? tr('Регистрация…', 'Registering…') : tr('Зарегистрироваться', 'Register') }}</span>
          <ArrowRight :size="18" />
        </button>
        <p class="secure-note"><ShieldCheck :size="14" /> {{ tr('Доступ активируется в соответствии с политикой сервера.', 'Access is activated according to server policy.') }}</p>
      </form>
    </section>
  </main>
</template>
