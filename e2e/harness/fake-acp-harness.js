#!/usr/bin/env node
'use strict';

const net = require('node:net');
const { randomUUID } = require('node:crypto');

const host = process.env.ACP_HOST || '127.0.0.1';
const port = Number(process.env.ACP_PORT || '8080');
let nextRequestId = 1;

class Connection {
	constructor(socket) {
		this.socket = socket;
		this.buffer = '';
		this.pending = new Map();
		this.sessions = new Map();

		socket.on('data', (chunk) => this.read(chunk));
		socket.on('error', (error) => this.rejectAll(error));
		socket.on('close', () => this.rejectAll(new Error('connection closed')));
	}

	read(chunk) {
		this.buffer += chunk.toString('utf8');
		for (;;) {
			const newline = this.buffer.indexOf('\n');
			if (newline === -1) return;

			const raw = this.buffer.slice(0, newline).trim();
			this.buffer = this.buffer.slice(newline + 1);
			if (raw === '') continue;
			this.dispatch(JSON.parse(raw));
		}
	}

	dispatch(message) {
		if (message.method !== undefined && message.id !== undefined) {
			void this.handleRequest(message);
			return;
		}

		const pending = this.pending.get(message.id);
		if (pending === undefined) return;
		this.pending.delete(message.id);
		if (message.error !== undefined) {
			pending.reject(new Error(message.error.message || `request failed: ${message.error.code}`));
		} else {
			pending.resolve(message.result || {});
		}
	}

	async handleRequest(message) {
		try {
			const params = isObject(message.params) ? message.params : {};
			let result;
			if (message.method === 'initialize') {
				result = {
					protocolVersion: 1,
					agentCapabilities: {},
					agentInfo: { name: 'fake-acp-harness', version: '0.0.0' },
				};
			} else if (message.method === 'session/new') {
				const sessionId = randomUUID();
				this.sessions.set(sessionId, { mcpServers: Array.isArray(params.mcpServers) ? params.mcpServers : [] });
				result = { sessionId };
			} else if (message.method === 'session/prompt') {
				result = await this.prompt(params);
			} else if (message.method === 'session/cancel') {
				result = {};
			} else {
				this.respondError(message.id, -32601, `unknown method: ${message.method}`);
				return;
			}
			this.respond(message.id, result);
		} catch (error) {
			this.respondError(message.id, -32000, error instanceof Error ? error.message : String(error));
		}
	}

	async prompt(params) {
		const sessionId = String(params.sessionId || '');
		const session = this.sessions.get(sessionId);
		if (session === undefined) throw new Error('unknown session');

		const text = promptText(params.prompt);
		if (text.includes('CALL_TOOL')) {
			const result = await this.callFirstTool(session);
			this.update(sessionId, `tool:${toolText(result)}`);
			return {};
		}

		this.update(sessionId, 'hello, world!');
		return {};
	}

	async callFirstTool(session) {
		const server = session.mcpServers.find((candidate) => candidate && candidate.type === 'acp');
		if (server === undefined) throw new Error('no acp mcp server configured');

		const connected = await this.request('mcp/connect', { acpId: server.id });
		const connectionId = String(connected.connectionId || '');
		if (connectionId === '') throw new Error('mcp/connect did not return connectionId');

		try {
			const listed = await this.request('mcp/message', { connectionId, method: 'tools/list', params: {} });
			const tools = Array.isArray(listed.tools) ? listed.tools : [];
			if (tools.length === 0) throw new Error('tools/list returned no tools');
			return await this.request('mcp/message', {
				connectionId,
				method: 'tools/call',
				params: { name: tools[0].name, arguments: { input: 'e2e' } },
			});
		} finally {
			await this.request('mcp/disconnect', { connectionId }).catch(() => {});
		}
	}

	request(method, params) {
		const id = `fake:${nextRequestId++}`;
		this.write({ jsonrpc: '2.0', id, method, params });
		return new Promise((resolve, reject) => {
			this.pending.set(id, { resolve, reject });
		});
	}

	update(sessionId, text) {
		this.write({
			jsonrpc: '2.0',
			method: 'session/update',
			params: {
				sessionId,
				update: {
					sessionUpdate: 'agent_message_chunk',
					content: { type: 'text', text },
				},
			},
		});
	}

	respond(id, result) {
		this.write({ jsonrpc: '2.0', id, result });
	}

	respondError(id, code, message) {
		this.write({ jsonrpc: '2.0', id, error: { code, message } });
	}

	write(message) {
		this.socket.write(`${JSON.stringify(message)}\n`);
	}

	rejectAll(error) {
		for (const pending of this.pending.values()) pending.reject(error);
		this.pending.clear();
	}
}

function promptText(prompt) {
	if (!Array.isArray(prompt)) return '';
	return prompt
		.map((part) => (part && part.type === 'text' && typeof part.text === 'string' ? part.text : ''))
		.join('');
}

function toolText(result) {
	const content = Array.isArray(result.content) ? result.content : [];
	const firstText = content.find((part) => part && part.type === 'text' && typeof part.text === 'string');
	return firstText ? firstText.text : JSON.stringify(result);
}

function isObject(value) {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

const server = net.createServer((socket) => new Connection(socket));
server.listen(port, host, () => {
	console.log(`fake-acp-harness listening on ${host}:${port}`);
});
