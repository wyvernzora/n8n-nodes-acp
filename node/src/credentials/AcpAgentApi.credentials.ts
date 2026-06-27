import type {
	ICredentialType,
	INodeProperties,
} from 'n8n-workflow';

export class AcpAgentApi implements ICredentialType {
	name = 'acpAgentApi';
	displayName = 'ACP Agent API';
	documentationUrl = 'https://github.com/wyvernzora/n8n-nodes-acp';

	properties: INodeProperties[] = [
		{
			displayName: 'Base URL',
			name: 'baseUrl',
			type: 'string',
			default: 'tcp://127.0.0.1:8080',
			placeholder: 'tcp://127.0.0.1:8080',
			required: true,
			description: 'ACP endpoint URL. Use tcp://127.0.0.1:8080 for the OpenCode sidecar',
		},
		{
			displayName: 'Note',
			name: 'note',
			type: 'notice',
			default: '',
			description: 'Authentication is configured in the harness sidecar for now',
		},
	];
}
