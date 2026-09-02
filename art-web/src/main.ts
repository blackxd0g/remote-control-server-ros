import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import './styles.css'
import './styles-extra.css'
import './membership.css'
import './users.css'
import './policies.css'
import './rustdesk-theme.css'

createApp(App).use(router).mount('#app')
