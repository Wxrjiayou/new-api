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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { buildApiParams, getDefaultTimeRange } from '../utils'

describe('getDefaultTimeRange', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 10, 20, 15, 20, 30))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  test('starts at 00:00:00 of the current day', () => {
    const { start } = getDefaultTimeRange()

    expect(start).toEqual(new Date(2026, 10, 20, 0, 0, 0, 0))
  })

  test('ends at 23:59:59 of the current day instead of the current time', () => {
    const { end } = getDefaultTimeRange()

    expect(end).toEqual(new Date(2026, 10, 20, 23, 59, 59, 0))
  })

  test('keeps the end on the same day when the current time is late at night', () => {
    vi.setSystemTime(new Date(2026, 10, 20, 23, 30, 0))

    const { end } = getDefaultTimeRange()

    expect(end).toEqual(new Date(2026, 10, 20, 23, 59, 59, 0))
  })

  test('sends the whole current day when the request has no time params', () => {
    const params = buildApiParams({
      page: 1,
      pageSize: 10,
      searchParams: {},
      isAdmin: false,
    })

    expect(params.start_timestamp).toBe(
      new Date(2026, 10, 20, 0, 0, 0).getTime() / 1000
    )
    expect(params.end_timestamp).toBe(
      new Date(2026, 10, 20, 23, 59, 59).getTime() / 1000
    )
  })
})
