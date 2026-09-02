<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { BookUser, Pencil, Plus, Search, Share2, Star, Trash2, X } from 'lucide-vue-next'
import { api } from '../api'
import { auth } from '../auth'
import { tr } from '../preferences'
import type { AddressBook, AddressBookEntry, AddressBookGrant, ManagedGroup, ManagedUser } from '../types'

const records = ref<AddressBook[]>([])
const entries = ref<AddressBookEntry[]>([])
const grants = ref<AddressBookGrant[]>([])
const users = ref<ManagedUser[]>([])
const groups = ref<ManagedGroup[]>([])
const selected = ref<AddressBook>()
const editingBook = ref<AddressBook>()
const editingEntry = ref<AddressBookEntry>()
const loading = ref(true)
const bookOpen = ref(false)
const entryOpen = ref(false)
const grantsOpen = ref(false)
const saving = ref(false)
const error = ref('')
const query = ref('')
const book = reactive<{name:string;kind:'personal'|'shared'}>({ name: '', kind: 'personal' })
const entry = reactive({ rustdesk_id: '', alias: '', folder: '', favourite: false })
const grant = reactive<{subject_type:'user'|'user_group';subject_id:string;permission:'read'|'write'}>({ subject_type: 'user', subject_id: '', permission: 'read' })

const filtered = computed(() => {
  const value = query.value.trim().toLowerCase()
  return value ? entries.value.filter((item) => [item.rustdesk_id, item.alias, item.folder].some((field) => field.toLowerCase().includes(value))) : entries.value
})
const canWrite = computed(() => selected.value?.permission === 'write' || selected.value?.permission === 'manage')
const userGroups = computed(() => groups.value.filter((value) => value.kind === 'user'))
const subjectName = (value: AddressBookGrant) => value.subject_type === 'user'
  ? users.value.find((user) => user.id === value.subject_id)?.display_name || users.value.find((user) => user.id === value.subject_id)?.username || value.subject_id
  : userGroups.value.find((group) => group.id === value.subject_id)?.name || value.subject_id

async function load() {
  loading.value = true
  error.value = ''
  try {
    records.value = await api.addressBooks()
    if (selected.value) {
      selected.value = records.value.find((value) => value.id === selected.value?.id)
      if (selected.value) await loadEntries()
      else entries.value = []
    }
  } catch (value) {
    fail(value, tr('Ошибка загрузки','Load failed'))
  } finally {
    loading.value = false
  }
}

async function loadEntries() {
  if (selected.value) entries.value = await api.addressBookEntries(selected.value.id)
}

async function selectBook(value: AddressBook) {
  selected.value = value
  query.value = ''
  await loadEntries()
}

function addBook() {
  editingBook.value = undefined
  Object.assign(book, { name: '', kind: 'personal' })
  bookOpen.value = true
}

function editBook(value: AddressBook) {
  editingBook.value = value
  Object.assign(book, { name: value.name, kind: value.kind })
  bookOpen.value = true
}

async function saveBook() {
  saving.value = true
  try {
    const value = editingBook.value
      ? await api.updateAddressBook(editingBook.value.id, { ...book })
      : await api.createAddressBook({ ...book })
    bookOpen.value = false
    await load()
    await selectBook(records.value.find((item) => item.id === value.id) ?? value)
  } catch (value) {
    fail(value, tr('Ошибка сохранения','Save failed'))
  } finally {
    saving.value = false
  }
}

async function removeBook(value: AddressBook) {
  if (!confirm(tr(`Удалить адресную книгу «${value.name}» и все её записи?`,`Delete address book “${value.name}” and all its entries?`))) return
  saving.value = true
  try {
    await api.deleteAddressBook(value.id)
    if (selected.value?.id === value.id) {
      selected.value = undefined
      entries.value = []
    }
    await load()
  } catch (cause) {
    fail(cause, tr('Ошибка удаления','Delete failed'))
  } finally {
    saving.value = false
  }
}

function addEntry() {
  editingEntry.value = undefined
  Object.assign(entry, { rustdesk_id: '', alias: '', folder: '', favourite: false })
  entryOpen.value = true
}

function editEntry(value: AddressBookEntry) {
  editingEntry.value = value
  Object.assign(entry, { rustdesk_id: value.rustdesk_id, alias: value.alias, folder: value.folder, favourite: value.favourite })
  entryOpen.value = true
}

async function saveEntry() {
  if (!selected.value) return
  saving.value = true
  try {
    if (editingEntry.value) await api.updateAddressBookEntry(selected.value.id, editingEntry.value.id, { ...entry })
    else await api.createAddressBookEntry(selected.value.id, { ...entry })
    entryOpen.value = false
    await loadEntries()
  } catch (value) {
    fail(value, tr('Ошибка сохранения записи','Entry save failed'))
  } finally {
    saving.value = false
  }
}

async function toggleFavourite(value: AddressBookEntry) {
  if (!selected.value || !canWrite.value) return
  try {
    await api.updateAddressBookEntry(selected.value.id, value.id, { ...value, favourite: !value.favourite })
    await loadEntries()
  } catch (cause) {
    fail(cause, tr('Ошибка обновления избранного','Favourite update failed'))
  }
}

async function removeEntry(value: AddressBookEntry) {
  if (!selected.value || !canWrite.value || !confirm(tr(`Удалить ${value.alias || value.rustdesk_id} из книги?`,`Remove ${value.alias || value.rustdesk_id} from the book?`))) return
  try {
    await api.deleteAddressBookEntry(selected.value.id, value.id)
    await loadEntries()
  } catch (cause) {
    fail(cause, tr('Ошибка удаления записи','Entry delete failed'))
  }
}

async function openGrants(value: AddressBook) {
  selected.value = value
  grantsOpen.value = true
  error.value = ''
  try {
    const [grantValues, userValues, groupValues] = await Promise.all([api.addressBookGrants(value.id), api.users(), api.groups()])
    grants.value = grantValues
    users.value = userValues
    groups.value = groupValues
    resetGrant()
  } catch (cause) {
    grantsOpen.value = false
    fail(cause, tr('Не удалось загрузить права доступа','Could not load access permissions'))
  }
}

function resetGrant() {
  Object.assign(grant, { subject_type: 'user', subject_id: '', permission: 'read' })
}

async function saveGrant() {
  if (!selected.value || !grant.subject_id) return
  saving.value = true
  try {
    await api.putAddressBookGrant(selected.value.id, { ...grant })
    grants.value = await api.addressBookGrants(selected.value.id)
    resetGrant()
  } catch (cause) {
    fail(cause, tr('Ошибка сохранения прав','Permission save failed'))
  } finally {
    saving.value = false
  }
}

async function removeGrant(value: AddressBookGrant) {
  if (!selected.value) return
  saving.value = true
  try {
    await api.deleteAddressBookGrant(selected.value.id, value.id)
    grants.value = await api.addressBookGrants(selected.value.id)
  } catch (cause) {
    fail(cause, tr('Ошибка удаления прав','Permission delete failed'))
  } finally {
    saving.value = false
  }
}

function fail(value: unknown, fallback: string) {
  error.value = value instanceof Error ? value.message : fallback
}

onMounted(load)
</script>

<template>
  <section class="section-heading">
    <div><p class="eyebrow">Address management</p><h2>{{tr('Адресные книги','Address books')}}</h2><p>{{tr('Личные книги изолированы владельцем, общие книги используют явные права пользователей и групп.','Personal books are owner-isolated; shared books use explicit user and group permissions.')}}</p></div>
    <button class="primary-button compact" @click="addBook"><Plus :size="17"/>{{tr('Создать книгу','Create book')}}</button>
  </section>
  <p v-if="error" class="alert-error">{{ error }}</p>
  <p v-if="loading" class="panel empty-state">{{tr('Загрузка…','Loading…')}}</p>
  <section v-else class="card-grid">
    <article v-for="value in records" :key="value.id" class="panel group-card selectable-card" :class="{selected:selected?.id===value.id}" @click="selectBook(value)">
      <span class="metric-icon"><BookUser :size="20"/></span>
      <div class="policy-content"><span class="badge neutral">{{ value.kind === 'shared' ? tr('Общая','Shared') : tr('Личная','Personal') }} · {{ value.permission }}</span><h3>{{ value.name }}</h3><p>{{ value.owner_user_id }}</p></div>
      <div class="policy-actions">
        <button v-if="value.kind==='shared' && value.can_manage" type="button" class="icon-btn" :title="tr('Права доступа','Access permissions')" @click.stop="openGrants(value)"><Share2 :size="15"/></button>
        <button v-if="value.can_manage" type="button" class="icon-btn" :title="tr('Редактировать','Edit')" @click.stop="editBook(value)"><Pencil :size="15"/></button>
        <button v-if="value.can_manage" type="button" class="icon-action" :title="tr('Удалить','Delete')" @click.stop="removeBook(value)"><Trash2 :size="15"/></button>
      </div>
    </article>
    <article v-if="!records.length" class="panel empty-state">{{tr('Доступных адресных книг пока нет.','No address books are available yet.')}}</article>
  </section>

  <section v-if="selected" class="panel table-panel address-entries">
    <div class="table-toolbar"><div><strong>{{ selected.name }}</strong><small> · {{ entries.length }} {{tr('устройств','devices')}} · {{ selected.permission }}</small></div><div class="address-actions"><div class="search-box"><Search :size="15"/><input v-model="query" :placeholder="tr('ID, имя или папка','ID, name, or folder')"/></div><button v-if="canWrite" class="primary-button compact" @click="addEntry"><Plus :size="15"/>{{tr('Добавить','Add')}}</button></div></div>
    <div class="table-scroll"><table><thead><tr><th>RustDesk ID</th><th>{{tr('Псевдоним','Alias')}}</th><th>{{tr('Папка','Folder')}}</th><th>{{tr('Избранное','Favourite')}}</th><th></th></tr></thead><tbody>
      <tr v-for="item in filtered" :key="item.id"><td>{{ item.rustdesk_id }}</td><td>{{ item.alias || '—' }}</td><td>{{ item.folder || '—' }}</td><td><button type="button" class="icon-btn" :disabled="!canWrite" @click="toggleFavourite(item)"><Star :size="15" :fill="item.favourite?'currentColor':'none'"/></button></td><td class="actions-cell"><template v-if="canWrite"><button type="button" class="icon-btn" @click="editEntry(item)"><Pencil :size="15"/></button><button type="button" class="icon-action" @click="removeEntry(item)"><Trash2 :size="15"/></button></template></td></tr>
      <tr v-if="!filtered.length"><td colspan="5" class="empty-cell">{{tr('Нет записей.','No entries.')}}</td></tr>
    </tbody></table></div>
  </section>

  <div v-if="bookOpen" class="modal-backdrop" @click.self="bookOpen=false"><form class="modal-card" @submit.prevent="saveBook"><div class="modal-heading"><h3>{{ editingBook ? tr('Редактирование книги','Edit book') : tr('Новая адресная книга','New address book') }}</h3><button type="button" class="icon-btn" @click="bookOpen=false"><X :size="20"/></button></div><label><span>{{tr('Название','Name')}}</span><input v-model="book.name" required minlength="2" maxlength="128"/></label><label><span>{{tr('Тип','Type')}}</span><select v-model="book.kind" :disabled="!auth.state.user?.is_admin"><option value="personal">{{tr('Личная','Personal')}}</option><option v-if="auth.state.user?.is_admin" value="shared">{{tr('Общая','Shared')}}</option></select></label><div class="modal-actions"><button type="button" class="secondary-button" @click="bookOpen=false">{{tr('Отмена','Cancel')}}</button><button class="primary-button compact" :disabled="saving">{{ saving ? tr('Сохранение…','Saving…') : tr('Сохранить','Save') }}</button></div></form></div>

  <div v-if="entryOpen" class="modal-backdrop" @click.self="entryOpen=false"><form class="modal-card" @submit.prevent="saveEntry"><div class="modal-heading"><h3>{{ editingEntry ? tr('Редактирование записи','Edit entry') : tr('Добавить устройство','Add device') }}</h3><button type="button" class="icon-btn" @click="entryOpen=false"><X :size="20"/></button></div><label><span>RustDesk ID</span><input v-model="entry.rustdesk_id" required minlength="3" maxlength="64"/></label><label><span>{{tr('Псевдоним','Alias')}}</span><input v-model="entry.alias" maxlength="128"/></label><label><span>{{tr('Папка','Folder')}}</span><input v-model="entry.folder" maxlength="128"/></label><label class="check-label"><input v-model="entry.favourite" type="checkbox"/><span>{{tr('Избранное','Favourite')}}</span></label><div class="modal-actions"><button type="button" class="secondary-button" @click="entryOpen=false">{{tr('Отмена','Cancel')}}</button><button class="primary-button compact" :disabled="saving">{{ saving ? tr('Сохранение…','Saving…') : tr('Сохранить','Save') }}</button></div></form></div>

  <div v-if="grantsOpen && selected" class="modal-backdrop" @click.self="grantsOpen=false"><section class="modal-card membership-modal"><div class="modal-heading"><div><p class="overline">Shared Address Book</p><h3>{{tr(`Доступ к «${selected.name}»`,`Access to “${selected.name}”`)}}</h3></div><button type="button" class="icon-btn" @click="grantsOpen=false"><X :size="20"/></button></div><p class="membership-note">{{tr('Чтение разрешает просмотр, изменение — добавление, редактирование и удаление записей. Управление книгой остаётся у владельца и администраторов.','Read allows viewing; write allows adding, editing, and deleting entries. Management remains with the owner and administrators.')}}</p><form class="form-grid grant-form" @submit.prevent="saveGrant"><label><span>{{tr('Получатель','Subject')}}</span><select v-model="grant.subject_type" required @change="grant.subject_id='' "><option value="user">{{tr('Пользователь','User')}}</option><option value="user_group">{{tr('Группа пользователей','User group')}}</option></select></label><label><span>{{tr('Пользователь или группа','User or group')}}</span><select v-model="grant.subject_id" required><option value="" disabled>{{tr('Выберите…','Select…')}}</option><option v-for="value in grant.subject_type==='user'?users:userGroups" :key="value.id" :value="value.id">{{ 'username' in value ? (value.display_name || value.username) : value.name }}</option></select></label><label><span>{{tr('Разрешение','Permission')}}</span><select v-model="grant.permission"><option value="read">{{tr('Только чтение','Read only')}}</option><option value="write">{{tr('Чтение и изменение','Read and write')}}</option></select></label><div class="grant-submit"><button class="primary-button compact" :disabled="saving || !grant.subject_id">{{tr('Применить','Apply')}}</button></div></form><div class="membership-list"><div v-for="value in grants" :key="value.id"><span class="table-avatar">{{ value.subject_type==='user'?'U':'G' }}</span><div><strong>{{ subjectName(value) }}</strong><small>{{ value.subject_type==='user'?tr('Пользователь','User'):tr('Группа пользователей','User group') }} · {{ value.permission==='write'?tr('чтение и изменение','read and write'):tr('только чтение','read only') }}</small></div><button type="button" class="icon-action" :title="tr('Удалить разрешение','Remove permission')" :disabled="saving" @click="removeGrant(value)"><Trash2 :size="15"/></button></div><p v-if="!grants.length" class="empty-cell">{{tr('Явных разрешений пока нет.','No explicit permissions yet.')}}</p></div></section></div>
</template>
