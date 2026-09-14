// Agent Matrix · 团战前端
// 单文件 Vue 3 SPA：列表 ↔ 详情 ↔ 开战模态框

const { createApp, ref, computed, onMounted, onBeforeUnmount, watch, nextTick } = Vue;

const api = {
  async get(path) {
    const r = await fetch(path, { credentials: 'include' });
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
    return r.json();
  },
  async post(path, body) {
    const r = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: body == null ? '' : JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
    return r.json().catch(() => ({}));
  },
};

function relTime(unix) {
  if (!unix) return '';
  const diff = Math.floor(Date.now() / 1000 - unix);
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  return `${Math.floor(diff / 86400)} 天前`;
}

function truncate(s, n) {
  if (!s) return '';
  return s.length > n ? s.slice(0, n) + '…' : s;
}

function renderMd(s) {
  if (!s) return '';
  if (typeof marked === 'undefined' || typeof DOMPurify === 'undefined') {
    return s.replace(/[<>&]/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;' }[c]));
  }
  return DOMPurify.sanitize(marked.parse(s));
}

createApp({
  setup() {
    const arenas = ref([]);
    const agents = ref([]);
    const agentMap = ref({});
    const activeArena = ref(null);
    const rounds = ref([]);
    const votes = ref([]);
    const tally = ref({});
    const verdict = ref('');
    const currentIndex = ref(0);

    const showCreate = ref(false);
    const createError = ref('');
    const working = ref(false);
    const voteMsg = ref('');
    const modalTilt = ref(Math.floor(Math.random() * 3));

    const form = ref({
      title: '',
      topic: '',
      participants: [],
      order_mode: 'random',
      manual_order: [],
      rounds_total: 3,
    });

    const canCreate = computed(() => {
      if (!form.value.title.trim() || !form.value.topic.trim()) return false;
      if (form.value.participants.length < 2) return false;
      if (form.value.order_mode === 'manual' &&
          form.value.manual_order.length !== form.value.participants.length) return false;
      return true;
    });

    const canVote = computed(() =>
      activeArena.value && (activeArena.value.status === 'voting' || activeArena.value.status === 'finished')
    );

    const currentVoterId = ref(''); // MVP 简化：admin 自己投

    function statusLabel(s) {
      return { pending: '待开战', running: '进行中', voting: '投票中', finished: '已结束', canceled: '已取消' }[s] || s;
    }
    function roundStatusLabel(s) {
      return { pending: '排队', delivered: '已派发', done: '完成', failed: '失败' }[s] || s;
    }
    function speakerIndex(aid) {
      if (!activeArena.value) return '?';
      const i = activeArena.value.participants.indexOf(aid);
      return i >= 0 ? i + 1 : '?';
    }
    function speakerRole(aid) { return 'p'; } // 团战里没有角色差异
    function agentName(id) {
      const a = agentMap.value[id];
      return a ? a.name : '（已删除）';
    }
    function agentPersona(id) {
      const a = agentMap.value[id];
      if (!a) return '';
      try {
        const m = typeof a.meta === 'string' ? JSON.parse(a.meta || '{}') : (a.meta || {});
        return m.persona || '';
      } catch { return ''; }
    }
    function winnerName(aid) {
      return agentName(aid);
    }
    function isWinner(aid) {
      return activeArena.value && activeArena.value.winner === aid;
    }

    async function loadAgents() {
      try {
        const r = await api.get('/api/agents');
        const list = r.agents || [];
        agentMap.value = Object.fromEntries(list.map(a => [a.id, a]));
        agents.value = list;
      } catch (e) { /* 未登录静默 */ }
    }
    async function loadArenas() {
      try {
        const r = await api.get('/api/arenas');
        arenas.value = r.arenas || [];
      } catch (e) { /* */ }
    }
    async function enterArena(a) {
      activeArena.value = a;
      currentIndex.value = arenas.value.findIndex(x => x.id === a.id);
      await loadDetail();
      history.replaceState(null, '', '?id=' + a.id);
    }
    async function leaveArena() {
      activeArena.value = null;
      rounds.value = [];
      votes.value = [];
      tally.value = {};
      verdict.value = '';
      currentIndex.value = 0;
      history.replaceState(null, '', '?');
    }
    async function loadDetail() {
      if (!activeArena.value) return;
      try {
        const r = await api.get('/api/arenas/' + activeArena.value.id);
        activeArena.value = r.arena;
        rounds.value = r.rounds || [];
        votes.value = r.votes || [];
        tally.value = r.tally || {};
        if (r.agents) {
          for (const [id, a] of Object.entries(r.agents)) agentMap.value[id] = a;
        }
        verdict.value = r.arena.verdict || '';
      } catch (e) { console.error(e); }
    }

    async function startArena() {
      if (!activeArena.value) return;
      working.value = true;
      try {
        const r = await api.post('/api/arenas/' + activeArena.value.id + '/start');
        activeArena.value = r.arena;
        rounds.value = r.rounds || [];
        await loadDetail();
      } catch (e) { alert('开战失败：' + e.message); }
      finally { working.value = false; }
    }

    async function castVote(target) {
      if (!activeArena.value) return;
      working.value = true;
      voteMsg.value = '';
      try {
        // MVP 简化：admin 用第一个 participant 视角投（真实场景会做 voter 选择器）
        const voter = currentVoterId.value || activeArena.value.participants[0];
        await api.post('/api/arenas/' + activeArena.value.id + '/vote', {
          voter_agent: voter,
          target_agent: target,
          reason: '',
        });
        voteMsg.value = '✅ 投票已记录';
        await loadDetail();
      } catch (e) { voteMsg.value = '❌ ' + e.message; }
      finally { working.value = false; }
    }

    function openCreate() {
      form.value = { title: '', topic: '', participants: [], order_mode: 'random', manual_order: [], rounds_total: 3 };
      createError.value = '';
      showCreate.value = true;
      if (agents.value.length === 0) loadAgents();
    }
    function closeCreate() { showCreate.value = false; }

    function toggleParticipant(id) {
      const idx = form.value.participants.indexOf(id);
      if (idx >= 0) {
        form.value.participants.splice(idx, 1);
        form.value.manual_order = form.value.manual_order.filter(x => x !== id);
      } else {
        form.value.participants.push(id);
        if (form.value.order_mode === 'manual') {
          form.value.manual_order.push(id);
        }
      }
    }
    function setOrderMode(m) {
      form.value.order_mode = m;
      if (m === 'manual') {
        form.value.manual_order = [...form.value.participants];
      } else {
        form.value.manual_order = [];
      }
    }
    function moveOrder(i, dir) {
      const j = i + dir;
      if (j < 0 || j >= form.value.manual_order.length) return;
      const arr = form.value.manual_order;
      [arr[i], arr[j]] = [arr[j], arr[i]];
    }
    function removeFromOrder(i) {
      const id = form.value.manual_order[i];
      form.value.manual_order.splice(i, 1);
      const p = form.value.participants.indexOf(id);
      if (p >= 0) form.value.participants.splice(p, 1);
    }

    async function createArena() {
      working.value = true;
      createError.value = '';
      try {
        const body = {
          title: form.value.title.trim(),
          topic: form.value.topic.trim(),
          participants: form.value.participants,
          order_mode: form.value.order_mode,
          rounds_total: form.value.rounds_total,
        };
        if (form.value.order_mode === 'manual') {
          body.manual_order = form.value.manual_order;
        }
        const a = await api.post('/api/arenas', body);
        showCreate.value = false;
        await loadArenas();
        await enterArena(a);
      } catch (e) { createError.value = e.message; }
      finally { working.value = false; }
    }

    // SSE
    let es;
    function connectSSE() {
      try {
        es = new EventSource('/api/events');
        es.onmessage = (ev) => {
          try {
            const data = JSON.parse(ev.data);
            const topic = data.topic || '';
            if (topic.startsWith('arena:')) {
              const kind = topic.split(':')[1];
              const id = topic.split(':')[2];
              if (!activeArena.value) loadArenas();
              else if (id === activeArena.value.id) loadDetail();
              else loadArenas();
            } else if (topic === 'arenas' || topic === 'tasks' || topic === 'agents') {
              loadArenas();
              if (activeArena.value) loadDetail();
            }
          } catch {}
        };
      } catch (e) { /* */ }
    }

    function restoreFromURL() {
      const m = location.search.match(/[?&]id=([^&]+)/);
      if (m) {
        const id = m[1];
        api.get('/api/arenas').then(r => {
          const a = (r.arenas || []).find(x => x.id === id);
          if (a) enterArena(a);
        }).catch(() => {});
      }
    }

    onMounted(async () => {
      await loadAgents();
      await loadArenas();
      restoreFromURL();
      connectSSE();
    });
    onBeforeUnmount(() => { if (es) es.close(); });

    // ──────── 角色卡 / HP / 战报分类 ────────
    const FIGURES = ['#fig-sword', '#fig-staff', '#fig-shield', '#fig-bow', '#fig-fist'];
    const FIGURE_COLORS = ['var(--warn)', 'var(--cyan)', 'var(--xp)', 'var(--warn-hi)', 'var(--red)'];
    function figureFor(idx) { return FIGURES[idx % FIGURES.length]; }
    function figureColor(idx) { return FIGURE_COLORS[idx % FIGURE_COLORS.length]; }
    const maxTally = computed(() => {
      const ts = Object.values(tally.value || {});
      return ts.length ? Math.max(...ts, 1) : 1;
    });
    function hpWidth(n, max) { return max > 0 ? Math.round((n / max) * 100) : 0; }
    function bubbleClass(r) {
      const body = (r.response || '').toLowerCase();
      if (r.reply_to_id) return 'is-quote';
      if (/(攻击|打脸|反驳|胡说|不对|错了|笑死|哈|呵呵|🙄|😒|笑)/.test(body)) return 'is-attack';
      if (/(赞成|同意|对|确实|赞|👍|支持)/.test(body)) return 'is-defend';
      return '';
    }
    const hasVoted = computed(() => currentVoterId.value !== null);

    return {
      arenas, agents, activeArena, rounds, votes, tally, verdict, currentIndex,
      showCreate, createError, form, working, voteMsg, modalTilt, canCreate, canVote, currentVoterId, hasVoted,
      statusLabel, roundStatusLabel, speakerIndex, speakerRole, agentName, agentPersona,
      winnerName, isWinner, relTime, truncate, renderMd,
      figureFor, figureColor, hpWidth, maxTally, bubbleClass,
      loadArenas, enterArena, leaveArena, startArena, castVote,
      openCreate, closeCreate, createArena, toggleParticipant, setOrderMode, moveOrder, removeFromOrder,
    };
  },
}).mount('#app');
