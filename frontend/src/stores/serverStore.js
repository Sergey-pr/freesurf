import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { Events } from '@wailsio/runtime'
import {
  GetServers,
  AddFromClipboard,
  RenameServer,
  DeleteServer,
  RefreshServer,
  PingNode,
  PingServer,
  GetConnState,
  Connect,
  Disconnect,
} from '../../bindings/freesurf/app.js'

export const useServerStore = defineStore('servers', () => {
  const servers = ref([])
  const conn = ref({ status: 'disconnected', nodeId: 0, message: '' })
  const selectedNodeId = ref(0)
  // nodeId -> latency: a number (ms), -1 (failed), or 'ping' (in progress).
  const pings = ref({})
  const refreshing = ref({})    // serverId -> bool
  const refreshErrors = ref({}) // serverId -> error string

  const isConnected = computed(() => conn.value.status === 'connected')
  const isConnecting = computed(() => conn.value.status === 'connecting')

  // The currently selected node across all servers, or null.
  const selectedNode = computed(() => {
    for (const s of servers.value) {
      const n = (s.nodes ?? []).find(n => n.id === selectedNodeId.value)
      if (n) return n
    }
    return null
  })

  const activeNode = computed(() => {
    if (conn.value.status !== 'connected') return null
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

  async function refreshServer(id) {
    await RefreshServer(id)
  }

  async function pingNode(id) {
    pings.value = { ...pings.value, [id]: 'ping' }
    const ms = await PingNode(id)
    pings.value = { ...pings.value, [id]: ms }
  }

  async function pingServer(id) {
    const server = servers.value.find(s => s.id === id)
    if (server) {
      const marking = {}
      for (const n of server.nodes ?? []) marking[n.id] = 'ping'
      pings.value = { ...pings.value, ...marking }
    }
    const res = await PingServer(id) // { nodeId: ms }
    pings.value = { ...pings.value, ...res }
  }

  async function connect(nodeId) {
    conn.value = await Connect(nodeId)
  }

  async function disconnect() {
    conn.value = await Disconnect()
  }

  // Toggle the connection using the selected node.
  async function toggleConnection() {
    if (conn.value.status === 'connected') {
      await disconnect()
    } else if (conn.value.status !== 'connecting' && selectedNodeId.value) {
      await connect(selectedNodeId.value)
    }
  }

  async function refreshConn() {
    conn.value = await GetConnState()
  }

  // Backend pushes updates when state changes from anywhere.
  Events.On('servers:refreshing', ev => {
    const id = ev?.data?.id
    if (id) refreshing.value = { ...refreshing.value, [id]: true }
  })
  Events.On('servers:refresh-done', ev => {
    const { id, error } = ev?.data ?? {}
    if (id) {
      refreshing.value = { ...refreshing.value, [id]: false }
      const errs = { ...refreshErrors.value }
      if (error) errs[id] = error; else delete errs[id]
      refreshErrors.value = errs
    }
  })
  Events.On('servers:changed', () => load())
  Events.On('vpn:state', ev => {
    conn.value = ev.data ?? conn.value
  })

  return {
    servers,
    conn,
    pings,
    refreshing,
    refreshErrors,
    isConnected,
    isConnecting,
    selectedNodeId,
    selectedNode,
    activeNode,
    load,
    select,
    addFromClipboard,
    renameServer,
    deleteServer,
    refreshServer,
    pingNode,
    pingServer,
    connect,
    disconnect,
    toggleConnection,
    refreshConn,
  }
})
