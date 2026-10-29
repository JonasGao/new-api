/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import type { UsageLog } from '../../../data/schema'
import type { LogOtherData } from '../../../types'
import { DetailsDialog } from '../details-dialog'

const { getLogRequestBody } = vi.hoisted(() => ({
  getLogRequestBody: vi.fn(),
}))

vi.mock('../../../api', () => ({ getLogRequestBody }))

const REQUEST_BODY = '{"model":"gpt-test","messages":[{"role":"user"}]}'

function makeLog(type: number, other: LogOtherData): UsageLog {
  return {
    id: 42,
    user_id: 1,
    created_at: 1,
    type,
    content: 'consume log',
    username: 'user',
    token_name: 'token',
    model_name: 'gpt-test',
    quota: 100,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 1,
    channel_name: '',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

const queryClients: QueryClient[] = []

function renderDetails(log: UsageLog, isAdmin: boolean): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClient.setQueryData(
    ['pricing'],
    { data: [], vendors: [] },
    { updatedAt: freshAt }
  )
  queryClients.push(queryClient)

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={log}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
  getLogRequestBody.mockReset()
})

describe('usage log request body', () => {
  test('given a stored body marker on a consume log, an admin sees the request body section', async () => {
    getLogRequestBody.mockResolvedValue({
      success: true,
      data: { body: REQUEST_BODY, truncated: false, size: 60 },
    })
    renderDetails(makeLog(2, { admin_info: { request_body_size: 60 } }), true)

    expect(
      await screen.findByText('Request Body', { selector: 'label' })
    ).toBeInTheDocument()
    expect(getLogRequestBody).toHaveBeenCalledWith(42)
  })

  test('given a stored body marker, a non-admin does not see the request body section', async () => {
    getLogRequestBody.mockResolvedValue({
      success: true,
      data: { body: REQUEST_BODY, truncated: false, size: 60 },
    })
    renderDetails(makeLog(2, { admin_info: { request_body_size: 60 } }), false)

    expect(screen.queryByText('Request Body', { selector: 'label' })).toBeNull()
    expect(getLogRequestBody).not.toHaveBeenCalled()
  })

  test('shows a loading indicator, then the stored body once the request resolves', async () => {
    let resolveBody: (value: unknown) => void = () => undefined
    getLogRequestBody.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveBody = resolve
        })
    )
    renderDetails(makeLog(5, { admin_info: { request_body_size: 60 } }), true)

    expect(screen.getByLabelText('Loading...')).toBeInTheDocument()

    await act(async () => {
      resolveBody({
        success: true,
        data: { body: REQUEST_BODY, truncated: false, size: 60 },
      })
    })

    expect(await screen.findByText(REQUEST_BODY)).toBeVisible()
    expect(screen.queryByLabelText('Loading...')).toBeNull()
  })

  test('given a truncated body, the admin sees the truncation note and original size', async () => {
    getLogRequestBody.mockResolvedValue({
      success: true,
      data: { body: REQUEST_BODY, truncated: true, size: 1048576 },
    })
    renderDetails(
      makeLog(2, {
        admin_info: {
          request_body_size: 1048576,
          request_body_truncated: true,
        },
      }),
      true
    )

    expect(
      await screen.findByText(
        'Request body was truncated to fit the storage limit'
      )
    ).toBeVisible()
    expect(screen.getByText('Original size')).toBeInTheDocument()
    expect(screen.getByText('1,048,576 bytes')).toBeVisible()
  })

  test('given a server failure, the admin sees the server message inline', async () => {
    getLogRequestBody.mockResolvedValue({
      success: false,
      message: 'No request body stored for this log',
    })
    renderDetails(makeLog(2, { admin_info: { request_body_size: 60 } }), true)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('No request body stored for this log')
  })
})
