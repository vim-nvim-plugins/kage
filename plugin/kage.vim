" kage.vim — AI coding assistant for Vim/Neovim
" Requires: Vim 8.2+ or Neovim, kage binary on PATH (or g:kage_binary)

if exists('g:loaded_kage') | finish | endif
let g:loaded_kage = 1

" ─── Configuration ────────────────────────────────────────────────────────────

let g:kage_binary       = get(g:, 'kage_binary',   'kage')
let g:kage_provider     = get(g:, 'kage_provider',  '')
let g:kage_model        = get(g:, 'kage_model',     '')
let g:kage_split        = get(g:, 'kage_split',     'vertical')
let g:kage_auto_approve = get(g:, 'kage_auto_approve', [])

" ─── Script state ─────────────────────────────────────────────────────────────

let s:job       = v:null
let s:chan       = v:null
let s:ready     = 0
let s:hist_buf  = -1
let s:input_buf = -1
let s:hist_win  = -1
let s:input_win = -1
let s:session   = ''
let s:streaming = 0
let s:pinned    = []
let s:edit_buf  = -1

" ─── Compat shim ──────────────────────────────────────────────────────────────

function! s:IsNeovim() abort
	return has('nvim')
endfunction

function! s:JobStart(cmd, opts) abort
	if s:IsNeovim()
		return jobstart(a:cmd, {
			\ 'on_stdout': a:opts.on_stdout,
			\ 'on_stderr': a:opts.on_stderr,
			\ 'on_exit':   a:opts.on_exit,
			\ })
	else
		return job_start(a:cmd, {
			\ 'out_cb':  a:opts.on_stdout,
			\ 'err_cb':  a:opts.on_stderr,
			\ 'exit_cb': a:opts.on_exit,
			\ 'mode':    'nl',
			\ })
	endif
endfunction

function! s:JobSend(line) abort
	if s:IsNeovim()
		call chansend(s:chan, a:line . "\n")
	else
		call ch_sendraw(s:chan, a:line . "\n")
	endif
endfunction

function! s:JobStop() abort
	if s:IsNeovim()
		if s:chan != v:null | call jobstop(s:chan) | endif
	else
		if s:job != v:null | call job_stop(s:job) | endif
	endif
endfunction

" ─── Binary lifecycle ─────────────────────────────────────────────────────────

function! s:EnsureStarted() abort
	if s:job != v:null | return | endif

	let l:opts = {
		\ 'on_stdout': function('s:OnStdout'),
		\ 'on_stderr': function('s:OnStderr'),
		\ 'on_exit':   function('s:OnExit'),
		\ }

	if s:IsNeovim()
		let s:chan = s:JobStart([g:kage_binary], l:opts)
		let s:job  = s:chan
	else
		let s:job  = s:JobStart([g:kage_binary], l:opts)
		let s:chan  = job_getchannel(s:job)
	endif

	call s:Send({'type': 'hello', 'version': '0.1.0',
		\ 'capabilities': ['chat', 'inline', 'diff']})

	if g:kage_provider !=# ''
		let l:cfg = {'type': 'config', 'provider': g:kage_provider}
		if g:kage_model !=# '' | let l:cfg.model = g:kage_model | endif
		call s:Send(l:cfg)
	endif
endfunction

function! s:Send(dict) abort
	call s:JobSend(json_encode(a:dict))
endfunction

" Block :q on the main editing window while kage panel is open.
function! s:GuardMainQuit() abort
	" Not our business if kage panel is not open
	if win_id2win(s:hist_win) == 0 && win_id2win(s:input_win) == 0
		return
	endif
	" Not our business if the closing window IS a kage pane
	let l:cur = win_getid()
	if l:cur == s:hist_win || l:cur == s:input_win
		return
	endif
	" Main editing window trying to quit while kage is open — abort quietly
	echohl WarningMsg
	echom 'kage: close the kage panel first (q in the kage panel)'
	echohl None
	" feedkeys <C-c> into the typeahead to cancel the :q without an error
	call feedkeys("\<C-c>", 'n')
endfunction

augroup kage_lifecycle
	autocmd!
	autocmd VimLeavePre * call s:Shutdown()
	" When any window is about to close, check if it's the main editor
	" while the kage panel is still open — if so, block it.
	autocmd QuitPre * call s:GuardMainQuit()
augroup END

function! s:Shutdown() abort
	if s:job == v:null | return | endif
	call s:Send({'type': 'shutdown'})
	call s:JobStop()
	let s:job   = v:null
	let s:chan   = v:null
	let s:ready = 0
endfunction

" ─── stdout / stderr handlers ─────────────────────────────────────────────────

function! s:OnStdout(job_or_id, data, ...) abort
	if s:IsNeovim()
		for l:line in a:data
			if l:line !=# '' | call s:DispatchLine(l:line) | endif
		endfor
	else
		if a:data !=# '' | call s:DispatchLine(a:data) | endif
	endif
endfunction

function! s:OnStderr(job_or_id, data, ...) abort
endfunction

function! s:OnExit(job_or_id, code, ...) abort
	let s:job   = v:null
	let s:chan   = v:null
	let s:ready = 0
	if a:code != 0
		echohl WarningMsg
		echom 'kage: binary exited with code ' . a:code
		echohl None
	endif
endfunction

" ─── Protocol dispatch ────────────────────────────────────────────────────────

function! s:DispatchLine(line) abort
	try
		let l:msg = json_decode(a:line)
	catch
		return
	endtry

	let l:type = get(l:msg, 'type', '')

	if l:type ==# 'ready'
		let s:ready = 1
	elseif l:type ==# 'stream_start'
		let s:streaming = 1
		call s:HistAppend('')
		call s:HistAppend(s:ModelLabel() . ':')
	elseif l:type ==# 'stream_text'
		call s:HistStream(get(l:msg, 'text', ''))
	elseif l:type ==# 'stream_thinking'
		call s:HistStream('[thinking] ' . get(l:msg, 'text', ''))
	elseif l:type ==# 'stream_end'
		let s:streaming = 0
		call s:HistAppend('')
		call s:HistScrollToBottom()
		call s:InputFocus()
	elseif l:type ==# 'error'
		call s:HistAppend('[error] ' . get(l:msg, 'message', ''))
		let s:streaming = 0
		call s:InputFocus()
	elseif l:type ==# 'edit_proposal'
		call s:HandleEditProposal(l:msg)
	elseif l:type ==# 'tool_call'
		call s:HandleToolCall(l:msg)
	endif
endfunction

" ─── Session ──────────────────────────────────────────────────────────────────

function! s:NewSession() abort
	let s:session = 's' . localtime() . '_' . rand()
endfunction

function! s:ActiveSession() abort
	if s:session ==# '' | call s:NewSession() | endif
	return s:session
endfunction

function! s:ModelLabel() abort
	let l:prov  = g:kage_provider !=# '' ? g:kage_provider : 'ollama'
	let l:model = g:kage_model    !=# '' ? g:kage_model    : 'llama3.2'
	return l:prov . '/' . l:model
endfunction

" ─── History buffer ───────────────────────────────────────────────────────────

function! s:SetupHistBuf() abort
	let s:hist_buf = bufnr('%')
	let s:hist_win = win_getid()

	setlocal buftype=nofile bufhidden=hide noswapfile nobuflisted
	setlocal filetype=kage
	setlocal wrap linebreak
	setlocal nonumber norelativenumber
	setlocal signcolumn=no
	setlocal nomodifiable
	" Let the history window resize freely with the terminal
	setlocal nowinfixheight

	" Statusline — reuse colorscheme's StatusLine group
	let &l:statusline = ' kage  ' . s:ModelLabel() . '%='

	" Syntax — link to standard groups so any colorscheme works
	syntax match KageYou       /^you:.*/
	syntax match KageModel     /^[a-z][a-z0-9._\/-]*:$/
	syntax match KageThinking  /^\[thinking\].*/
	syntax match KageError     /^\[error\].*/
	syntax match KageFence     /^```\w*$/
	syntax match KageSeparator /^─\+$/

	" Link to colorscheme groups — no hardcoded colors
	highlight default link KageYou      Question
	highlight default link KageModel    Identifier
	highlight default link KageThinking Comment
	highlight default link KageError    ErrorMsg
	highlight default link KageFence    NonText
	highlight default link KageSeparator NonText

	" Block every mutation key in normal and visual mode
	for l:k in ['d','dd','D','c','cc','C','x','X','r','R','s','S',
		\          'i','I','a','A','o','O','p','P','u','<C-r>']
		execute 'nnoremap <buffer> ' . l:k . ' <NOP>'
	endfor
	for l:k in ['d','D','c','C','x','p','P','r','s','u']
		execute 'vnoremap <buffer> ' . l:k . ' <NOP>'
	endfor

	nnoremap <buffer> q     :call <SID>KageClose()<CR>
	nnoremap <buffer> <C-c> :call <SID>CancelStream()<CR>
	nnoremap <buffer> <Tab> :call <SID>InputFocus()<CR>

	" Block all window splitting and moving — kage manages its own layout
	nnoremap <buffer> <C-w>v     <NOP>
	nnoremap <buffer> <C-w>s     <NOP>
	nnoremap <buffer> <C-w>n     <NOP>
	nnoremap <buffer> <C-w><C-v> <NOP>
	nnoremap <buffer> <C-w><C-s> <NOP>
	nnoremap <buffer> <C-w><C-n> <NOP>

	augroup kage_hist_close
		autocmd! * <buffer>
		" Closing history pane closes both
		autocmd QuitPre <buffer> call s:KageClose()
	augroup END

	" Write header
	call s:HistUnlock()
	call setline(1, 'kage')
	call append(1, [repeat('─', 40), ''])
	call s:HistLock()
endfunction

function! s:HistUnlock() abort
	if bufexists(s:hist_buf)
		call setbufvar(s:hist_buf, '&modifiable', 1)
	endif
endfunction

function! s:HistLock() abort
	if bufexists(s:hist_buf)
		call setbufvar(s:hist_buf, '&modifiable', 0)
	endif
endfunction

function! s:HistAppend(text) abort
	if !bufexists(s:hist_buf) | return | endif
	call s:HistUnlock()
	call appendbufline(s:hist_buf, '$', a:text)
	call s:HistLock()
endfunction

function! s:HistStream(text) abort
	if !bufexists(s:hist_buf) | return | endif
	let l:parts = split(a:text, '\n', 1)
	let l:last  = get(getbufline(s:hist_buf, '$'), 0, '')
	call s:HistUnlock()
	call setbufline(s:hist_buf, '$', l:last . l:parts[0])
	for l:part in l:parts[1:]
		call appendbufline(s:hist_buf, '$', l:part)
	endfor
	call s:HistLock()
	call s:HistScrollToBottom()
endfunction

function! s:HistScrollToBottom() abort
	if win_id2win(s:hist_win) > 0
		call win_execute(s:hist_win, 'normal! G')
	endif
endfunction

" ─── Input buffer ─────────────────────────────────────────────────────────────

function! s:SetupInputBuf() abort
	let s:input_buf = bufnr('%')
	let s:input_win = win_getid()

	setlocal buftype=nofile bufhidden=hide noswapfile nobuflisted
	setlocal filetype=kageinput
	setlocal nonumber norelativenumber
	setlocal signcolumn=no
	setlocal nowrap
	" No ~ for empty lines
	setlocal fillchars+=eob:\ 
	" Fix height so terminal resize doesn't collapse it
	setlocal winfixheight

	" Statusline — match the normal statusline look
	let &l:statusline = '  <Esc> normal  ·  <CR> send  ·  <C-s> send (insert)  ·  <Tab> scroll history  %='

	" Normal mode: Enter sends
	nnoremap <buffer> <CR>   :call <SID>SendInput()<CR>
	nnoremap <buffer> <C-c>  :call <SID>CancelStream()<CR>
	nnoremap <buffer> q      :call <SID>KageClose()<CR>
	nnoremap <buffer> <Tab>  :call <SID>HistFocus()<CR>

	" Insert mode: Enter is just a newline (default), Ctrl-s sends
	inoremap <buffer> <C-s>  <Esc>:call <SID>SendInput()<CR>
	inoremap <buffer> <C-c>  <Esc>:call <SID>CancelStream()<CR>

	" Block splitting from input pane too
	nnoremap <buffer> <C-w>v     <NOP>
	nnoremap <buffer> <C-w>s     <NOP>
	nnoremap <buffer> <C-w>n     <NOP>
	nnoremap <buffer> <C-w><C-v> <NOP>
	nnoremap <buffer> <C-w><C-s> <NOP>
	nnoremap <buffer> <C-w><C-n> <NOP>
	inoremap <buffer> <C-w>v     <NOP>
	inoremap <buffer> <C-w>s     <NOP>

	augroup kage_input_close
		autocmd! * <buffer>
		" Closing input pane closes both
		autocmd QuitPre <buffer> call s:KageClose()
	augroup END

	call setline(1, '')
	startinsert
endfunction

function! s:InputFocus() abort
	if win_id2win(s:input_win) > 0
		call win_gotoid(s:input_win)
		startinsert!
	endif
endfunction

function! s:HistFocus() abort
	if win_id2win(s:hist_win) > 0
		call win_gotoid(s:hist_win)
	endif
endfunction

function! s:SendInput() abort
	if !bufexists(s:input_buf) | return | endif

	" Collect all lines from input buffer
	let l:lines  = getbufline(s:input_buf, 1, '$')
	let l:prompt = trim(join(l:lines, "\n"))

	if l:prompt ==# ''
		call s:InputFocus()
		return
	endif

	if s:streaming
		echom 'kage: streaming — <C-c> to cancel'
		call s:InputFocus()
		return
	endif

	" Clear input buffer
	call deletebufline(s:input_buf, 2, '$')
	call setbufline(s:input_buf, 1, '')

	" Echo into history with a blank line above for breathing room
	call s:HistAppend('you: ' . split(l:prompt, "\n")[0])
	" If multiline, indent continuation lines
	for l:line in split(l:prompt, "\n")[1:]
		call s:HistAppend('     ' . l:line)
	endfor

	call s:EnsureStarted()

	call s:Send({
		\ 'type':    'chat',
		\ 'session': s:ActiveSession(),
		\ 'prompt':  l:prompt,
		\ 'context': s:GatherContext(),
		\ })

	call s:InputFocus()
endfunction

" ─── Panel layout ─────────────────────────────────────────────────────────────

function! s:OpenPanel() abort
	" Already open — just focus input
	if win_id2win(s:hist_win) > 0 && win_id2win(s:input_win) > 0
		call s:InputFocus()
		return
	endif

	" Outer split
	if g:kage_split ==# 'horizontal'
		split
	elseif g:kage_split ==# 'tab'
		tabedit
	else
		vsplit
	endif

	" History pane (top, takes remaining space)
	enew
	call s:SetupHistBuf()

	" Input pane (bottom, fixed 10 lines)
	belowright split
	resize 10
	enew
	call s:SetupInputBuf()
endfunction

function! s:KageClose() abort
	if win_id2win(s:input_win) > 0
		call win_execute(s:input_win, 'hide')
	endif
	if win_id2win(s:hist_win) > 0
		call win_execute(s:hist_win, 'hide')
	endif
endfunction

function! s:CancelStream() abort
	if !s:streaming | return | endif
	call s:Send({'type': 'cancel', 'session': s:ActiveSession()})
endfunction

" ─── Context ──────────────────────────────────────────────────────────────────

function! s:GatherContext() abort
	let l:ctx = []
	let l:eb = s:EditBuf()
	if l:eb > 0 | let l:ctx += [s:BufferContextBlock(l:eb)] | endif
	for l:pin in s:pinned | let l:ctx += [l:pin] | endfor
	return l:ctx
endfunction

function! s:EditBuf() abort
	if s:edit_buf > 0 && bufexists(s:edit_buf) | return s:edit_buf | endif
	for l:b in range(1, bufnr('$'))
		if buflisted(l:b) && bufloaded(l:b)
			\ && l:b != s:hist_buf && l:b != s:input_buf
			return l:b
		endif
	endfor
	return -1
endfunction

function! s:BufferContextBlock(bufnr) abort
	return {
		\ 'kind':     'current_buffer',
		\ 'path':     expand('#' . a:bufnr . ':p'),
		\ 'language': getbufvar(a:bufnr, '&filetype'),
		\ 'content':  join(getbufline(a:bufnr, 1, '$'), "\n"),
		\ }
endfunction

" ─── Commands ─────────────────────────────────────────────────────────────────

command! -nargs=? Kage              call s:KageOpen(<q-args>)
command! -nargs=0 KageChat          call s:KageChatToggle()
command! -range -nargs=? KageEdit   call s:KageEdit(<line1>, <line2>, <q-args>)
command! -nargs=? KageInline        call s:KageEdit(line('.'), line('.'), <q-args>)
command! -nargs=0 KageCancel        call s:CancelStream()
command! -nargs=1 KageProvider      call s:SetProvider(<q-args>)
command! -nargs=1 KageAddFile       call s:AddFile(<q-args>)
command! -nargs=0 KageStatus        call s:Status()
command! -nargs=0 KageDebug         call s:Debug()

function! s:KageOpen(prompt) abort
	let s:edit_buf = bufnr('%')
	call s:EnsureStarted()
	call s:OpenPanel()
	if a:prompt !=# ''
		call setbufline(s:input_buf, 1, a:prompt)
		call s:SendInput()
	endif
endfunction

function! s:KageChatToggle() abort
	if win_id2win(s:hist_win) > 0
		call s:KageClose()
	else
		call s:KageOpen('')
	endif
endfunction

function! s:KageEdit(line1, line2, prompt) abort
	let s:edit_buf = bufnr('%')
	let l:path = expand('%:p')
	call s:EnsureStarted()
	call s:OpenPanel()
	call s:Send({
		\ 'type':      'inline',
		\ 'session':   s:ActiveSession(),
		\ 'prompt':    a:prompt !=# '' ? a:prompt : 'Edit this selection',
		\ 'file':      l:path,
		\ 'selection': {
		\   'kind':     'selection',
		\   'path':     l:path,
		\   'start':    a:line1,
		\   'end':      a:line2,
		\   'content':  join(getline(a:line1, a:line2), "\n"),
		\   'language': &filetype,
		\ },
		\ 'context': [],
		\ })
endfunction

function! s:SetProvider(name) abort
	call s:EnsureStarted()
	call s:Send({'type': 'config', 'provider': a:name})
	let g:kage_provider = a:name
	let &l:statusline = ' kage  ' . s:ModelLabel() . '%='
	echo 'kage: switched to ' . a:name
endfunction

function! s:AddFile(path) abort
	let l:full = fnamemodify(a:path, ':p')
	if !filereadable(l:full)
		echohl ErrorMsg | echom 'kage: cannot read ' . l:full | echohl None
		return
	endif
	call add(s:pinned, {
		\ 'kind':     'file',
		\ 'path':     l:full,
		\ 'content':  join(readfile(l:full), "\n"),
		\ 'language': fnamemodify(l:full, ':e'),
		\ })
	echo 'kage: pinned ' . l:full
endfunction

function! s:Status() abort
	echo printf('kage  provider=%s  model=%s  session=%s  streaming=%d  pinned=%d',
		\ (g:kage_provider !=# '' ? g:kage_provider : '(default)'),
		\ (g:kage_model    !=# '' ? g:kage_model    : '(default)'),
		\ s:session, s:streaming, len(s:pinned))
endfunction

function! s:Debug() abort
	echo 'job='       . string(s:job)
	echo 'chan='      . string(s:chan)
	echo 'ready='     . s:ready
	echo 'session='   . s:session
	echo 'hist_buf='  . s:hist_buf
	echo 'input_buf=' . s:input_buf
	if s:job != v:null && !s:IsNeovim()
		echo 'job_status=' . job_status(s:job)
	endif
endfunction

" ─── Stubs ────────────────────────────────────────────────────────────────────

function! s:HandleEditProposal(msg) abort
	call s:HistAppend('[edit proposal: ' . get(a:msg, 'file', '') . ']')
	call s:HistAppend(get(a:msg, 'new', ''))
endfunction

function! s:HandleToolCall(msg) abort
	call s:HistAppend('[tool: ' . get(a:msg, 'name', '?') . ']')
endfunction
