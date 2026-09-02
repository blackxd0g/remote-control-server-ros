<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useRoute, useRouter } from 'vue-router'
import { ArrowRight, Globe2, KeyRound, LockKeyhole, MoonStar, ShieldCheck, UserPlus, UserRound } from 'lucide-vue-next'
import BrandMark from '../components/BrandMark.vue'
import { appVersion } from '../version'
import AuthStarfield from '../components/AuthStarfield.vue'
import { auth } from '../auth'
import { APIError, api } from '../api'
import { preferences, t, tr, type Locale, type Theme } from '../preferences'

const route = useRoute()
const router = useRouter()
const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')
const verificationCode = ref('')
const requires2FA = ref(false)
const oidcProviders = ref<{name:string}[]>([])
const oidcBusy = ref(false)
const registrationEnabled=ref(false)
const adminMode=computed(()=>route.meta.portal==='admin')

onMounted(async()=>{const [providers,registration]=await Promise.allSettled([api.loginOptions(),api.registrationOptions()]);if(providers.status==='fulfilled')oidcProviders.value=providers.value;if(registration.status==='fulfilled')registrationEnabled.value=registration.value.enabled})
const wait = (milliseconds:number) => new Promise((resolve)=>window.setTimeout(resolve,milliseconds))
async function oidcLogin(provider:string){error.value='';oidcBusy.value=true;try{const flow=await api.beginOIDC(provider);window.open(flow.url,'_blank','noopener,noreferrer');for(let attempt=0;attempt<180;attempt+=1){await wait(1000);const result=await api.pollOIDC(flow.code,flow.id,flow.uuid);if('access_token' in result&&result.access_token){auth.accept(result);if(adminMode.value&&result.user.role!=='admin'){await auth.logout();error.value=tr('Эта страница предназначена только для администраторов.','This page is for administrators only.');return}await router.replace(adminMode.value?'/admin':'/account');return}}error.value=tr('Время ожидания входа через OIDC истекло.','OIDC sign-in timed out.')}catch(reason){error.value=reason instanceof Error?reason.message:tr('Не удалось войти через OIDC','OIDC login failed')}finally{oidcBusy.value=false}}

async function submit() {
  error.value = ''
  loading.value = true
  try {
    await auth.login(username.value.trim().toLowerCase(), password.value, verificationCode.value)
    if(adminMode.value&&auth.state.user?.role!=='admin'){await auth.logout();error.value=tr('Эта страница предназначена только для администраторов.','This page is for administrators only.');return}
    const fallback=adminMode.value?'/admin':'/account'
    const returnTo = typeof route.query.returnTo === 'string' && route.query.returnTo.startsWith(adminMode.value?'/admin':'/account') ? route.query.returnTo : fallback
    await router.replace(returnTo)
  } catch (reason) {
    if (reason instanceof APIError && reason.payload.requires_2fa === true) {
      requires2FA.value = true
      error.value = verificationCode.value ? tr('Неверный или уже использованный код подтверждения.','The verification code is invalid or already used.') : tr('Введите шестизначный код из приложения-аутентификатора или recovery-код.','Enter a six-digit authenticator code or a recovery code.')
    } else if (reason instanceof APIError && reason.payload.mfa_enrollment_required === true) {
      error.value = tr('Для этой учётной записи обязательна двухфакторная защита. Обратитесь к администратору для завершения настройки.','Two-factor protection is required for this account. Contact an administrator to complete enrollment.')
    } else {
      error.value = reason instanceof Error ? reason.message : tr('Не удалось войти','Unable to sign in')
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <AuthStarfield />
    <section class="login-context">
      <div class="login-brand"><BrandMark /><div><strong>Remote Control Server</strong><span>RouterOS</span></div></div>
      <div class="login-message">
        <span class="eyebrow"><ShieldCheck :size="15" /> {{tr('Self-hosted платформа управления','Self-hosted control plane')}}</span>
        <h1>{{tr('Безопасный доступ начинается до подключения.','Secure access starts before the connection.')}}</h1>
        <p>{{tr('Личность, серверная сессия и политика соединения проверяются до rendezvous или авторизации relay.','Identity, server-side sessions and connection policy are verified before rendezvous or relay authorization.')}}</p>
        <ul>
          <li><span><LockKeyhole :size="18" /></span><div><strong>{{tr('Обязательный вход','Login enforced')}}</strong><small>{{tr('HBBS запрещает анонимные удалённые подключения.','Anonymous remote connections are denied at HBBS.')}}</small></div></li>
          <li><span><KeyRound :size="18" /></span><div><strong>{{tr('Отзываемые сессии','Revocable sessions')}}</strong><small>{{tr('Выход и изменения учётной записи применяются к новым подключениям.','Logout and account changes apply to new connections.')}}</small></div></li>
        </ul>
      </div>
      <small class="login-version">Remote Control Server · v{{appVersion}} · Compatible with the RustDesk client protocol · <a href="https://boosty.to/blackxdog" target="_blank" rel="noopener noreferrer">created by blackxdog</a></small>
    </section>
    <section class="login-panel">
      <form class="login-card" @submit.prevent="submit">
        <div class="login-preferences"><label><MoonStar :size="15"/><select :value="preferences.state.theme" @change="preferences.setTheme(($event.target as HTMLSelectElement).value as Theme)"><option value="system">{{t('system')}}</option><option value="dark">{{t('dark')}}</option><option value="light">{{t('light')}}</option></select></label><label><Globe2 :size="15"/><select :value="preferences.state.locale" @change="preferences.setLocale(($event.target as HTMLSelectElement).value as Locale)"><option value="ru">RU</option><option value="en">EN</option></select></label></div>
        <div class="mobile-brand"><BrandMark /><strong>Remote Control Server</strong></div>
        <nav class="auth-portal-nav" :aria-label="tr('Выбор способа входа','Choose sign-in portal')">
          <RouterLink to="/admin/login" :class="{active:adminMode}"><ShieldCheck :size="15"/><span>{{tr('Администратор','Administrator')}}</span></RouterLink>
          <RouterLink to="/account/login" :class="{active:!adminMode}"><UserRound :size="15"/><span>{{tr('Пользователь','User')}}</span></RouterLink>
          <RouterLink v-if="registrationEnabled" to="/register"><UserPlus :size="15"/><span>{{tr('Регистрация','Register')}}</span></RouterLink>
        </nav>
        <p class="overline">{{adminMode?tr('Панель администратора','Administration portal'):tr('Личный кабинет пользователя','User account')}}</p>
        <h2>{{ t('welcomeBack') }}</h2>
        <p class="form-intro">{{ t('signInIntro') }}</p>
        <label><span>{{ t('username') }}</span><div class="input-wrap"><UserRound :size="18" /><input v-model="username" autocomplete="username" required autofocus placeholder="admin" /></div></label>
        <label><span>{{ t('password') }}</span><div class="input-wrap"><LockKeyhole :size="18" /><input v-model="password" type="password" autocomplete="current-password" required placeholder="••••••••••••" /></div></label>
        <label v-if="requires2FA"><span>{{tr('Код подтверждения','Verification code')}}</span><div class="input-wrap"><ShieldCheck :size="18" /><input v-model="verificationCode" maxlength="11" autocomplete="one-time-code" required autofocus :placeholder="tr('000000 или XXXXX-XXXXX','000000 or XXXXX-XXXXX')" /></div></label>
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
        <button class="primary-button" :disabled="loading"><span>{{ loading ? t('signingIn') : t('signIn') }}</span><ArrowRight :size="18" /></button>
        <template v-if="oidcProviders.length"><div class="login-divider"><span>{{tr('или','or')}}</span></div><button v-for="provider in oidcProviders" :key="provider.name" type="button" class="secondary-button oidc-button" :disabled="oidcBusy" @click="oidcLogin(provider.name)"><Globe2 :size="17"/>{{oidcBusy?tr('Ожидание авторизации…','Waiting for authorization…'):`${tr('Войти через','Sign in with')} ${provider.name}`}}</button></template>
        <p class="secure-note"><ShieldCheck :size="14" /> {{tr('Учётные данные обрабатываются вашим self-hosted API.','Credentials are handled by your self-hosted API.')}}</p>
      </form>
    </section>
  </main>
</template>
