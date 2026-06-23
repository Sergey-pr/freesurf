import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { Events } from '@wailsio/runtime'
import {
  GetServers,
  AddFromClipboard,
  RenameServer,
  DeleteServer,
  GetConnState,
  Connect,
  Disconnect,
} from '../../bindings/free-surf/app.js'

export const useServerStore = defineStore('servers', () => {
  const servers = ref([])
  const conn = ref({ connected: false, nodeId: 0 })
  const selectedNodeId = ref(0)

  // The currently selected node across all servers, or null.
  const selectedNode = computed(() => {
    for (const s of servers.value) {
      const n = (s.nodes ?? []).find(n => n.id === selectedNodeId.value)
      if (n) return n
    }
    return null
  })

  const activeNode = computed(() => {
    if (!conn.value.connected) return null
    for (const s of servers.value) {
      const n = (s.nodes ?? []).find(n => n.id === conn.value.nodeId)
      if (n) return n
    }
    return null
  })

  async function load() {
    servers.value = (await GetServers()) ?? []
    // Keep selection valid; default to the first available node.
    if (!selectedNode.value) {
      selectedNodeId.value = firstNodeId()
    }
  }

  function firstNodeId() {
    for (const s of servers.value) {
      if (s.nodes && s.nodes.length) return s.nodes[0].id
    }
    return 0
  }

  function select(nodeId) {
    selectedNodeId.value = nodeId
  }

  async function addFromClipboard() {
    const created = await AddFromClipboard()
    await load()
    if (created && created.nodes && created.nodes.length) {
      selectedNodeId.value = created.nodes[0].id
    }
    return created
  }

  async function renameServer(id, name) {
    await RenameServer(id, name)
    await load()
  }

  async function deleteServer(id) {
    await DeleteServer(id)
    await load()
  }

  async function connect(nodeId) {
    conn.value = await Connect(nodeId)
  }

  async function disconnect() {
    conn.value = await Disconnect()
  }

  // Toggle the connection using the selected node.
  async function toggleConnection() {
    if (conn.value.connected) {
      await disconnect()
    } else if (selectedNodeId.value) {
      await connect(selectedNodeId.value)
    }
  }

  async function refreshConn() {
    conn.value = await GetConnState()
  }

  // Backend pushes updates when state changes from anywhere.
  Events.On('servers:changed', () => load())
  Events.On('vpn:state', ev => {
    conn.value = ev.data?.[0] ?? ev.data ?? conn.value
  })

  return {
    servers,
    conn,
    selectedNodeId,
    selectedNode,
    activeNode,
    load,
    select,
    addFromClipboard,
    renameServer,
    deleteServer,
    connect,
    disconnect,
    toggleConnection,
    refreshConn,
  }
})
