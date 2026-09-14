/* Agent Battle · Mission Control
   Agent 接入 / 列表 / 状态 / 踢出
*/
const { createApp, ref, computed, onMounted, onBeforeUnmount } = Vue;

createApp({
  setup() {
    const agents = ref([]);
    const working = ref(false);
    const newAgentName = ref('');
    const enrollCmd = ref('');
    const copyMsg = ref('');

    async function api(path, opts = {}) {
      const r = await fetch(path, {
        headers: { 'Content-Type': 'application/json' },
        ...opts,
      });
      if (!r.ok) {
        const t = await r.text();
        throw new Error(`${r.status} ${t.slice(0, 200)}`);
      }
      if (r.status === 204) return null;
      return r.json();
    }

    async function loadAgents() {
      try {
        const d = await api('/api/agents');
        agents.value = Array.isArray(d) ? d : (d.agents || []);
      } catch (e) {
        console.error('loadAgents failed', e);
      }
    }

    async function createEnrollment() {
      if (!newAgentName.value.trim()) return;
      working.value = true;
      try {
        const d = await api('/api/enrollments', {
          method: 'POST',
          body: JSON.stringify({ name: newAgentName.value.trim() }),
        });
        const base = location.origin;
        enrollCmd.value = `curl -sSL "${base}/setup.sh" -o setup.sh && chmod +x setup.sh && ./setup.sh --token ${d.token || d.enrollment_token || ''} --name "${newAgentName.value.trim()}"`;
        newAgentName.value = '';
      } catch (e) {
        alert('生成失败: ' + e.message);
      } finally {
        working.value = false;
      }
    }

    function resetEnroll() {
      enrollCmd.value = '';
      copyMsg.value = '';
    }

    async function copyCmd() {
      try {
        await navigator.clipboard.writeText(enrollCmd.value);
        copyMsg.value = '已复制 ✓';
        setTimeout(() => (copyMsg.value = ''), 2000);
      } catch (e) {
        copyMsg.value = '复制失败,手动复制';
      }
    }

    async function downloadSetup() {
      try {
        const r = await fetch('/setup.sh');
        const blob = await r.blob();
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = 'setup.sh';
        a.click();
        URL.revokeObjectURL(a.href);
      } catch (e) {
        alert('下载失败: ' + e.message);
      }
    }

    async function kickAgent(a) {
      if (!confirm(`确定踢出 "${a.name}" 吗?\n该 Agent 的 session 会被注销,需要重新接入。`)) return;
      working.value = true;
      try {
        await api(`/api/agents/${a.id}`, { method: 'DELETE' });
        await loadAgents();
      } catch (e) {
        alert('踢出失败: ' + e.message);
      } finally {
        working.value = false;
      }
    }

    const onlineCount = computed(() => {
      const now = Math.floor(Date.now() / 1000);
      return agents.value.filter(a => (now - (a.last_seen || 0)) < 60).length;
    });

    function statusLabel(s) {
      if (s === 'online') return '在线';
      if (s === 'offline') return '离线';
      return '僵尸';
    }
    function relTime(ts) {
      if (!ts) return '从未';
      const diff = Math.floor(Date.now() / 1000) - ts;
      if (diff < 60) return `${diff} 秒前`;
      if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
      if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
      return `${Math.floor(diff / 86400)} 天前`;
    }
    function agentStatus(a) {
      if (!a.last_seen) return 'zombie';
      const diff = Math.floor(Date.now() / 1000) - a.last_seen;
      if (diff < 60) return 'online';
      if (diff < 300) return 'offline';
      return 'zombie';
    }

    let pollTimer = null;
    onMounted(() => {
      loadAgents();
      pollTimer = setInterval(loadAgents, 10000);
    });
    onBeforeUnmount(() => { if (pollTimer) clearInterval(pollTimer); });

    return {
      agents, working, newAgentName, enrollCmd, copyMsg, onlineCount,
      loadAgents, createEnrollment, resetEnroll, copyCmd, downloadSetup, kickAgent,
      statusLabel, relTime, agentStatus,
    };
  },
}).mount('#app');
