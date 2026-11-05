// vx-bot/NOTE: Generated from Go contracts. Run the backend contracts:generate target.
export type PermissionMode = 'ask' | 'safe' | 'full'

export interface CompanionConnection {
  requestId: string
  code: string
  expiresAt: string
}

export interface CompanionConnectionRequest {
  requestId: string
}

export interface CompanionConnectionResult {
  status: string
  session?: CompanionSession
}

export interface CompanionSession {
  token: string
  expiresAt: string
}

export interface WebDocument {
  pageId?: string
  url?: string
  title?: string
  content?: string
  headings?: Array<string>
  sources?: Array<WebSource>
  retrievedAt?: string
}

export interface WebSearchResult {
  sources: Array<WebSource>
}

export interface WebSource {
  id: string
  url: string
  title: string
  domain: string
  retrievedAt: string
  snippet?: string
}

export interface Task {
  label: string
  done: boolean
}

export interface ToolCall {
  signature?: string
  id: string
  name: string
  arguments: string
}

export interface Attachment {
  id: string
  name: string
  mimeType: string
  size: number
  text?: string
}

export interface Message {
  id?: string
  role: string
  content: string
  attachments?: Array<Attachment>
  toolCalls?: Array<ToolCall>
  toolCallId?: string
  rating?: string
  tasks?: Array<Task>
  outfitLocked?: boolean
  profileId?: string
  persona?: string
  outfit?: string
}

export interface ActivityAppearance {
  persona?: string
  outfit?: string
}

export interface RunRequest {
  activityProfiles?: Record<string, ActivityAppearance>
  conversationId: string
  messages: Array<Message>
  model?: string
  fast: boolean
  permission: PermissionMode
  title?: string
  expectedVersion?: number
}

export interface ApprovalDecision {
  runId: string
  callId: string
  approved: boolean
}

export interface Usage {
  inputTokens: number
  outputTokens: number
  totalTokens: number
}

export interface StreamEvent {
  type: string
  runId: string
  conversationId: string
  sequence: number
  delta?: string
  callId?: string
  name?: string
  input?: unknown
  output?: unknown
  reason?: string
  message?: string
  status?: string
  parentRunId?: string
  usage?: Usage
}

export interface ModelInfo {
  id: string
  name: string
  provider: string
  modalities?: Array<string>
  contextWindow?: number
  fast?: boolean
}

export interface Conversation {
  id: string
  title: string
  messages: Array<Message>
  updatedAt: number
  version: number
  parentId?: string
  branchFrom?: string
  summary?: string
}

export interface ConversationPage {
  items: Array<Conversation>
  nextCursor?: string
}

export interface RenameRequest {
  title: string
}

export interface FeedbackRequest {
  rating: string
}

export interface TasksRequest {
  tasks: Array<Task>
}

export interface BranchRequest {
  messageId: string
  title?: string
}

export interface Checkpoint {
  summary?: string
  id: string
  conversationId: string
  label: string
  messages: Array<Message>
  createdAt: number
}

export interface CheckpointRequest {
  label: string
}

export interface RunInfo {
  id: string
  conversationId: string
  status: string
  lastSequence: number
  parentId?: string
  usage: Usage
  createdAt: number
  updatedAt: number
}

export interface PluginInfo {
  required: boolean
  error?: string
  name: string
  kind: string
  enabled: boolean
  status: string
  dependencies: Array<string>
}

export interface PluginUpdate {
  enabled: boolean
}

export interface Principal {
  id: string
  name?: string
  roles?: Array<string>
}

export interface SessionInfo {
  user: Principal
  loginUrl?: string
  expiresAt?: number
}

export interface AuthSession {
  canLogout: boolean
  methods: Array<LoginMethod>
  user?: Principal
  loginEnabled: boolean
}

export interface LoginMethod {
  id: string
  name: string
}

export interface Capabilities {
  version: string
  images: boolean
  pdf: boolean
  transcription: boolean
  speech: boolean
  oidc: boolean
  sessions: boolean
  replay: boolean
  plugins: boolean
  memory: boolean
  checkpoints: boolean
}

export interface Memory {
  id: string
  text: string
  tags?: Array<string>
  score?: number
  createdAt: number
}

export interface MemoryRequest {
  text: string
  tags?: Array<string>
}

export interface Transcript {
  text: string
}

export interface SpeechRequest {
  text: string
  voice?: string
}

export interface ClientAction {
  name: string
  path?: string
  query?: string
}

export interface APIError {
  code: string
  message: string
}

export interface ProviderSettings {
  searchEnabled: boolean
  oauthAvailable: boolean
  authMethod?: string
  projectId?: string
  id: string
  name: string
  connected: boolean
  models: Array<ModelInfo>
  transcriptionModel?: string
  speechModel?: string
}

export interface ProviderKeyRequest {
  apiKey: string
}

export interface ProviderSearchRequest {
  enabled: boolean
}

export interface ProviderOAuthRequest {
  projectId: string
  returnTo: string
}

export interface ProviderAuthorization {
  url: string
}
