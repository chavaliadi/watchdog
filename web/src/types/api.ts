export interface ApiErrorPayload {
  code: string;
  message: string;
  details: unknown;
}

export interface ApiErrorEnvelope {
  error: ApiErrorPayload;
}
