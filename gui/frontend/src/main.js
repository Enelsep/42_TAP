import './style.css';

import {
    Attack, Chat, Connect, Disconnect, Drop, GroupCreate, GroupInvite, GroupJoin,
    GroupLeave, Inventory, Look, Move, Quest, Quests, Status, Take, Talk, Who,
} from '../wailsjs/go/main/App';
import { EventsOn } from '../wailsjs/runtime/runtime';

import bar from './assets/images/bar.png';
import boss from './assets/images/boss.png';
import camp from './assets/images/camp.png';
import city from './assets/images/city.png';
import door from './assets/images/door.png';
import nest from './assets/images/nest.png';
import shop from './assets/images/shop.png';
import square from './assets/images/square.png';
import start from './assets/images/start.png';
import suburbs from './assets/images/suburbs.png';

// Room id -> backdrop. Keyed by the canonical ids the server puts on the wire,
// so a room the world adds later simply shows no art instead of breaking.
const BACKDROPS = {
    'loc.bar': bar, 'loc.bossroom': boss, 'loc.camp': camp, 'loc.city': city,
    'loc.door': door, 'loc.nest': nest, 'loc.shop': shop, 'loc.square': square,
    'loc.start': start, 'loc.suburbs': suburbs,
};

const DIRECTIONS = ['north', 'south', 'east', 'west'];
const SCOPES = ['ROOM', 'GLOBAL', 'GROUP'];

const state = {
    name: '',
    room: null,
    players: [],
    items: [],
    npcs: [],
    inventory: [],
    chat: { ROOM: [], GLOBAL: [], GROUP: [] },
    scope: 'ROOM',
    backdrop: null,
};

const $ = (id) => document.getElementById(id);

// Canonical ids are what the protocol speaks; people read display names.
const pretty = (id) => {
    const bare = String(id).replace(/^(item|npc|loc)\./, '').replace(/_/g, ' ');
    return bare.charAt(0).toUpperCase() + bare.slice(1);
};

const message = (e) => String(e && e.message ? e.message : e);

// --- surfaces -------------------------------------------------------------

function logLine(text, isError = false) {
    const line = document.createElement('div');
    line.textContent = text;
    if (isError) line.className = 'err';
    $('log').append(line);
    $('log').scrollTop = $('log').scrollHeight;
}

function toast(text, isError = false) {
    const el = document.createElement('div');
    el.className = isError ? 'toast err' : 'toast';
    el.textContent = text;
    $('toasts').append(el);
    setTimeout(() => el.remove(), 4000);
}

// guard runs a backend call and turns a rejected promise — an ERR reply, or a
// dropped connection — into a toast plus a log line, never an unhandled error.
async function guard(fn) {
    try {
        return { ok: true, value: await fn() };
    } catch (e) {
        toast(message(e), true);
        logLine(message(e), true);
        return { ok: false };
    }
}

// --- rendering ------------------------------------------------------------

let showingA = false;

function setBackdrop(roomID) {
    if (state.backdrop === roomID) return;
    const url = BACKDROPS[roomID];
    if (!url) return;
    state.backdrop = roomID;

    const next = showingA ? $('bg-b') : $('bg-a');
    const current = showingA ? $('bg-a') : $('bg-b');
    next.style.backgroundImage = `url("${url}")`;
    next.classList.add('visible');
    current.classList.remove('visible');
    showingA = !showingA;
}

function listInto(node, entries, emptyText) {
    node.replaceChildren();
    if (!entries.length) {
        const li = document.createElement('li');
        li.className = 'empty';
        li.textContent = emptyText;
        node.append(li);
        return;
    }
    for (const entry of entries) {
        const li = document.createElement('li');
        const label = document.createElement('span');
        label.textContent = entry.label;
        li.append(label);
        if (entry.action) {
            const button = document.createElement('button');
            button.textContent = entry.action;
            button.onclick = entry.onClick;
            li.append(button);
        }
        node.append(li);
    }
}

function renderRoom() {
    const room = state.room;
    if (!room) return;

    $('hud-room').textContent = room.name || '';
    $('room-desc').textContent = room.description || '';
    $('count-room').textContent = state.players.length;
    setBackdrop(room.id);

    listInto($('room-npcs'), state.npcs.map((id) => ({
        label: pretty(id),
        action: 'talk',
        onClick: () => talkTo(id),
    })), 'nobody here');

    listInto($('room-items'), state.items.map((id) => ({
        label: pretty(id),
        action: 'take',
        onClick: () => takeItem(id),
    })), 'nothing on the ground');

    const exits = room.exits || {};
    for (const button of $('compass').querySelectorAll('button')) {
        button.disabled = !exits[button.dataset.dir];
    }
}

function renderInventory() {
    listInto($('inventory'), state.inventory.map((id) => ({
        label: pretty(id),
        action: 'drop',
        onClick: () => dropItem(id),
    })), 'empty-handed');
}

function renderChat() {
    const log = $('chat-log');
    log.replaceChildren();
    const lines = state.chat[state.scope];
    if (!lines.length) {
        const empty = document.createElement('div');
        empty.className = 'empty';
        empty.textContent = `no ${state.scope.toLowerCase()} chatter yet`;
        log.append(empty);
        return;
    }
    for (const { who, text } of lines) {
        const line = document.createElement('div');
        const name = document.createElement('span');
        name.className = 'who';
        name.textContent = `${who} `;
        line.append(name, document.createTextNode(text));
        log.append(line);
    }
    log.scrollTop = log.scrollHeight;
}

function renderHealth({ hp, max_hp }) {
    const max = max_hp || 100;
    $('hp-fill').style.width = `${Math.max(0, Math.min(100, (hp / max) * 100))}%`;
    $('hp-text').textContent = `${hp} / ${max}`;
}

// --- refreshes ------------------------------------------------------------

// Re-LOOK after anything that can change the room, so the view always matches
// what the server thinks is there.
async function refreshRoom() {
    const look = await guard(Look);
    if (!look.ok) return;
    state.room = look.value.room;
    state.players = look.value.players || [];
    state.items = look.value.items || [];
    state.npcs = look.value.npcs || [];
    renderRoom();
}

async function refreshInventory() {
    const inventory = await guard(Inventory);
    if (!inventory.ok) return;
    state.inventory = inventory.value || [];
    renderInventory();
}

async function refreshStatus() {
    const status = await guard(Status);
    if (status.ok) renderHealth(status.value);
}

async function refreshWho() {
    const who = await guard(Who);
    if (who.ok) $('count-server').textContent = who.value;
}

// --- picker ---------------------------------------------------------------

// Resolves to a chosen value, a typed one, or null. Typing is what lets a
// player use a display name where the buttons offer canonical ids.
function pick(title, choices, placeholder = '…or type a name') {
    return new Promise((resolve) => {
        const done = (value) => {
            $('picker').hidden = true;
            $('picker-form').onsubmit = null;
            $('picker-cancel').onclick = null;
            resolve(value);
        };

        $('picker-title').textContent = title;
        $('picker-choices').replaceChildren(...choices.map(({ label, value }) => {
            const button = document.createElement('button');
            button.textContent = label;
            button.onclick = () => done(value);
            return button;
        }));

        const input = $('picker-input');
        input.value = '';
        input.placeholder = placeholder;
        $('picker-form').onsubmit = (e) => {
            e.preventDefault();
            if (input.value.trim()) done(input.value.trim());
        };
        $('picker-cancel').onclick = () => done(null);

        $('picker').hidden = false;
        input.focus();
    });
}

const asChoices = (ids) => ids.map((id) => ({ label: pretty(id), value: id }));

// --- actions --------------------------------------------------------------

async function move(dir) {
    const moved = await guard(() => Move(dir));
    if (!moved.ok) return;
    logLine(`moved ${dir}`);
    await refreshRoom();
}

async function takeItem(id) {
    const taken = await guard(() => Take(id));
    if (!taken.ok) return;
    logLine(`took ${pretty(taken.value)}`);
    await Promise.all([refreshRoom(), refreshInventory()]);
}

async function dropItem(id) {
    const dropped = await guard(() => Drop(id));
    if (!dropped.ok) return;
    logLine(`dropped ${pretty(dropped.value)}`);
    await Promise.all([refreshRoom(), refreshInventory()]);
}

async function talkTo(id) {
    const said = await guard(() => Talk(id));
    if (said.ok) logLine(`${pretty(id)}: ${said.value}`);
}

async function attack(id) {
    const hit = await guard(() => Attack(id));
    if (!hit.ok) return;
    const { attacker_hp, target_hp, damage, status } = hit.value;
    logLine(`hit ${pretty(id)} for ${damage} — it has ${target_hp} hp, you have ${attacker_hp} (${status})`);
    renderHealth({ hp: attacker_hp, max_hp: 100 });
    await refreshRoom();
}

async function askQuest(id) {
    const quest = await guard(() => Quest(id));
    if (quest.ok) logLine(`${pretty(id)} offers ${quest.value.quest_id} (${quest.value.status}): ${quest.value.description}`);
}

async function listQuests() {
    const quests = await guard(Quests);
    if (!quests.ok) return;
    const entries = quests.value || [];
    if (!entries.length) {
        logLine('no quests yet');
        return;
    }
    for (const q of entries) {
        logLine(`quest ${q.quest_id}: ${q.status}${q.progress ? ` (${q.progress})` : ''}`);
    }
}

async function groupMenu() {
    const action = await pick('Group', [
        { label: 'create', value: 'create' },
        { label: 'invite', value: 'invite' },
        { label: 'join', value: 'join' },
        { label: 'leave', value: 'leave' },
    ], 'no typing needed');

    if (action === 'create') {
        const created = await guard(GroupCreate);
        if (created.ok) logLine(`group created: ${created.value}`);
    } else if (action === 'invite') {
        const who = await pick('Invite whom?', asChoices(state.players.filter((p) => p !== state.name)), 'player name');
        if (who && (await guard(() => GroupInvite(who))).ok) logLine(`invited ${who}`);
    } else if (action === 'join') {
        const group = await pick('Join which group?', [], 'group id, or the name of whoever invited you');
        if (group) {
            const joined = await guard(() => GroupJoin(group));
            if (joined.ok) logLine(`joined group ${joined.value}`);
        }
    } else if (action === 'leave') {
        if ((await guard(GroupLeave)).ok) logLine('left the group');
    }
}

const ACTIONS = {
    LOOK: async () => {
        await refreshRoom();
        logLine(`${state.room?.name ?? 'nowhere'} — ${state.players.length} here`);
    },
    MOVE: async () => {
        const exits = Object.keys(state.room?.exits || {});
        const dir = await pick('Move where?', exits.map((d) => ({ label: d, value: d })), 'direction');
        if (dir) await move(dir);
    },
    STATUS: async () => {
        const status = await guard(Status);
        if (!status.ok) return;
        renderHealth(status.value);
        logLine(`${status.value.hp}/${status.value.max_hp} hp — ${status.value.status}`);
    },
    TAKE: async () => {
        const id = await pick('Take what?', asChoices(state.items), 'item name or id');
        if (id) await takeItem(id);
    },
    DROP: async () => {
        const id = await pick('Drop what?', asChoices(state.inventory), 'item name or id');
        if (id) await dropItem(id);
    },
    TALK: async () => {
        const id = await pick('Talk to whom?', asChoices(state.npcs), 'npc name or id');
        if (id) await talkTo(id);
    },
    ATTACK: async () => {
        const id = await pick('Attack whom?', asChoices(state.npcs), 'npc name or id');
        if (id) await attack(id);
    },
    QUEST: async () => {
        const id = await pick('Ask whom for a quest?', asChoices(state.npcs), 'npc name or id');
        if (id) await askQuest(id);
    },
    QUESTS: listQuests,
    WHO: async () => {
        await refreshWho();
        logLine(`${$('count-server').textContent} players on the server`);
    },
    GROUP: groupMenu,
};

// --- server events --------------------------------------------------------

EventsOn('tap:evt', (event) => {
    if (event.scope === 'STATS') {
        $('count-server').textContent = event.players ?? 0;
        return;
    }
    if (event.kind === 'CHAT' && SCOPES.includes(event.scope)) {
        state.chat[event.scope].push({ who: event.player, text: event.message });
        renderChat();
        return;
    }
    if (event.kind === 'PRESENCE') {
        logLine(`${event.player} ${event.presence === 'ENTER' ? 'arrives' : 'leaves'}`);
        refreshRoom();
        return;
    }
    if (event.scope === 'GROUP') {
        logLine(`group: ${event.player} ${event.kind.toLowerCase()}`);
        return;
    }
    logLine(event.raw || 'unknown event'); // another group's extension: show, never crash
});

EventsOn('tap:disconnected', () => {
    toast('connection lost', true);
    showConnect();
});

// --- session --------------------------------------------------------------

function showConnect() {
    $('hud').hidden = true;
    $('connect').hidden = false;
    $('connect-error').textContent = '';
}

async function enterWorld(addr, name) {
    try {
        await Connect(addr, name);
    } catch (e) {
        $('connect-error').textContent = message(e);
        return;
    }

    state.name = name;
    state.backdrop = null;
    state.chat = { ROOM: [], GLOBAL: [], GROUP: [] };
    $('hud-name').textContent = name;
    $('log').replaceChildren();
    $('connect').hidden = true;
    $('hud').hidden = false;

    renderChat();
    await Promise.all([refreshRoom(), refreshInventory(), refreshStatus(), refreshWho()]);
    logLine(`connected to ${addr} as ${name}`);
}

// --- wiring ---------------------------------------------------------------

$('connect-form').onsubmit = (e) => {
    e.preventDefault();
    const addr = $('addr').value.trim();
    const name = $('player-name').value.trim();
    if (!addr || !name) {
        $('connect-error').textContent = 'server and name are both required';
        return;
    }
    enterWorld(addr, name);
};

$('btn-quit').onclick = async () => {
    await guard(Disconnect);
    showConnect();
};

for (const button of $('compass').querySelectorAll('button')) {
    button.onclick = () => move(button.dataset.dir);
}

for (const button of $('actions').querySelectorAll('button')) {
    button.onclick = () => ACTIONS[button.dataset.action]();
}

for (const tab of $('chat-tabs').querySelectorAll('button')) {
    tab.onclick = () => {
        state.scope = tab.dataset.scope;
        for (const other of $('chat-tabs').querySelectorAll('button')) {
            other.classList.toggle('active', other === tab);
        }
        renderChat();
    };
}

$('chat-form').onsubmit = async (e) => {
    e.preventDefault();
    const text = $('chat-input').value.trim();
    if (!text) return;
    $('chat-input').value = '';
    await guard(() => Chat(state.scope, text)); // the echoed EVT is what renders it
};

// Keep the compass usable from the keyboard, except while typing.
document.addEventListener('keydown', (e) => {
    if (e.target.tagName === 'INPUT' || $('hud').hidden) return;
    const dir = { ArrowUp: 'north', ArrowDown: 'south', ArrowLeft: 'west', ArrowRight: 'east' }[e.key];
    if (dir && state.room?.exits?.[dir]) move(dir);
});

// Decode every backdrop up front so a move never flashes an empty frame.
for (const url of Object.values(BACKDROPS)) {
    new Image().src = url;
}

renderChat();
