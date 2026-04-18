const statusEl = document.getElementById('status');
const timelineEl = document.getElementById('timeline');
const conversationSelect = document.getElementById('conversationSelect');
const refreshButton = document.getElementById('refreshButton');
const template = document.getElementById('itemTemplate');
let stream;
let currentItems = [];

function setStatus(text) {
  statusEl.textContent = text;
}

function formatMeta(item) {
  const parts = [new Date(item.created_at).toLocaleString(), item.kind.toUpperCase()];
  if (item.agent_id) parts.push(`from ${item.agent_id}`);
  if (item.peer_agent_id) parts.push(`to ${item.peer_agent_id}`);
  if (item.task_key) parts.push(`task ${item.task_key}`);
  if (item.ack_state) parts.push(`ack ${item.ack_state}`);
  return parts.join(' • ');
}

function renderItems(items) {
  currentItems = [...items];
  timelineEl.innerHTML = '';
  for (const item of items) {
    const node = template.content.firstElementChild.cloneNode(true);
    node.classList.add(item.kind);
    node.querySelector('.meta').textContent = formatMeta(item);
    node.querySelector('.body').textContent = item.summary ? `${item.summary}\n${item.body || ''}`.trim() : (item.body || '');
    timelineEl.appendChild(node);
  }
}

async function loadConversations() {
  const response = await fetch('/api/timeline/conversations');
  const conversations = await response.json();
  conversationSelect.innerHTML = '';
  for (const conv of conversations) {
    const option = document.createElement('option');
    option.value = conv.id;
    option.textContent = `${conv.title} (${conv.status})`;
    conversationSelect.appendChild(option);
  }
  if (!conversationSelect.value && conversations[0]) {
    conversationSelect.value = conversations[0].id;
  }
  return conversations;
}

async function loadItems() {
  const id = conversationSelect.value;
  if (!id) {
    renderItems([]);
    setStatus('No conversations found yet. POST to /api/timeline/ingest to seed the portal.');
    return;
  }
  const response = await fetch(`/api/timeline/items?conversation_id=${encodeURIComponent(id)}&limit=200`);
  const items = await response.json();
  renderItems(items);
  setStatus(`Showing ${items.length} timeline item(s) for ${id}`);
}

function connectStream() {
  if (stream) stream.close();
  const id = conversationSelect.value;
  if (!id) return;
  stream = new EventSource(`/api/timeline/stream?conversation_id=${encodeURIComponent(id)}`);
  stream.addEventListener('snapshot', (event) => {
    const payload = JSON.parse(event.data);
    renderItems(payload.items || []);
  });
  stream.addEventListener('timeline_item', (event) => {
    const payload = JSON.parse(event.data);
    currentItems.push(payload.item);
    currentItems.sort((a, b) => new Date(a.created_at) - new Date(b.created_at));
    renderItems(currentItems);
  });
  stream.onerror = () => setStatus(`Realtime reconnecting for ${id}…`);
}

conversationSelect.addEventListener('change', async () => {
  await loadItems();
  connectStream();
});
refreshButton.addEventListener('click', async () => {
  await loadConversations();
  await loadItems();
  connectStream();
});

(async function init() {
  try {
    await loadConversations();
    await loadItems();
    connectStream();
  } catch (error) {
    console.error(error);
    setStatus(`Failed to load portal: ${error.message}`);
  }
})();
