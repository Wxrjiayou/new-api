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
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'

import { LogsFilterToolbar } from '../logs-filter-toolbar'

function Fixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <LogsFilterToolbar
      table={table}
      primaryFilters={<input aria-label='Model' />}
      advancedFilters={<input aria-label='Token Name' />}
      hasActiveFilters={false}
      onReset={() => {}}
      onSearch={() => {}}
    />
  )
}

afterEach(() => {
  cleanup()
})

it('shows advanced filters on first render without clicking Expand', () => {
  render(<Fixture />)

  expect(screen.getByLabelText('Token Name')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Collapse' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
})

it('hides advanced filters when Collapse is clicked', async () => {
  const user = userEvent.setup()
  render(<Fixture />)

  await user.click(screen.getByRole('button', { name: 'Collapse' }))

  expect(screen.queryByLabelText('Token Name')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Expand' })).toHaveAttribute(
    'aria-expanded',
    'false'
  )
})
