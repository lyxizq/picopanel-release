<template>
  <div class="files-page" @click="ctxMenu.show = false">
    <!-- 左栏分组服务器（手机端为下拉选择器）。宽度可拖拽分隔条调节、
         单击分隔条收起/展开（useSidePanel 记忆） -->
    <ServerTree
      v-show="isMobile || !side.collapsed"
      :style="isMobile ? null : { width: side.width + 'px', flexShrink: 0 }"
      :servers="servers"
      :groups="groups"
      :selected-id="activeID"
      @select="openTab"
    />
    <div
      v-if="!isMobile"
      class="side-divider"
      :class="{ on: side.resizing }"
      :title="side.collapsed ? '单击展开侧栏' : '拖拽调节宽度 · 单击收起侧栏'"
      @mousedown="side.startDrag"
    />

    <div
      class="files-main"
      :class="{ 'drop-over': dragOver }"
      @dragover.prevent="onDragOver"
      @dragleave="onDragLeave"
      @drop.prevent="onDrop"
    >
      <template v-if="cur">
        <!-- 服务器标签栏（每台服务器一个标签，双击关闭；胶囊样式与文档页一致） -->
        <div class="tab-bar">
          <div
            v-for="t in tabs"
            :key="t.id"
            class="tab"
            :class="{ on: t.id === activeID }"
            :title="t.name + '（双击关闭）'"
            @click="activeID = t.id"
            @dblclick="closeTab(t)"
          >
            <span class="tab-name">{{ t.name }}</span>
            <el-icon class="tab-close" @click.stop="closeTab(t)"><Close /></el-icon>
          </div>
        </div>

        <!-- 工具栏（参考宝塔布局分两行）：
             第一行 导航：向上/后退 + 路径框 + 刷新 + 右侧名称搜索（可勾「包含子目录」递归）；
             第二行 操作：上传/新建▾/内容搜索/收藏夹▾/终端/磁盘用量，次要操作收进右侧更多▾ -->
        <div class="fm-nav">
          <div class="tb-nav">
            <el-tooltip content="向上（上一层目录）" placement="bottom" :hide-after="0">
              <el-button size="small" type="primary" :disabled="path === '/'" @click="goUp">
                <el-icon><Top /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip content="后退" placement="bottom" :hide-after="0">
              <el-button size="small" type="primary" :disabled="!cur || cur.hidx <= 0" @click="goBack">
                <el-icon><Back /></el-icon>
              </el-button>
            </el-tooltip>
          </div>
          <div class="path-box" :class="{ editing: pathEditing }">
            <div v-show="!pathEditing" class="crumbs">
              <span class="crumb root" :class="{ cur: path === '/' }" title="/" @click="go('/')">根目录</span>
              <template v-for="seg in crumbSegs" :key="seg.path">
                <span class="crumb-sep">/</span>
                <span class="crumb" :class="{ cur: seg.path === path }" @click="go(seg.path)">{{ seg.name }}</span>
              </template>
            </div>
            <input
              ref="pathInputEl"
              v-model="pathInput"
              class="path-input"
              placeholder="输入绝对路径后回车跳转"
              @focus="pathEditing = true"
              @blur="onPathBlur"
              @keyup.enter="goPathInput"
              @keydown.esc="pathInputEl?.blur()"
            />
            <span class="path-copy" title="复制路径" @mousedown.prevent @click="copyPath">
              <el-icon><CopyDocument /></el-icon>
            </span>
          </div>
          <el-tooltip content="刷新" placement="bottom" :hide-after="0">
            <el-button size="small" type="primary" @click="refreshView"><el-icon><Refresh /></el-icon></el-button>
          </el-tooltip>
          <input ref="upInput" type="file" multiple hidden @change="onUploadPicked" />
        </div>
        <div class="fm-toolbar">
          <el-tooltip content="上传文件到当前目录" placement="bottom" :hide-after="0">
            <el-button size="small" type="success" @click="pickUpload">
              <el-icon><Upload /></el-icon>
            </el-button>
          </el-tooltip>
          <el-dropdown trigger="click" @command="onCreate">
            <el-button size="small" type="primary">
              <el-icon><FolderAdd /></el-icon><el-icon class="tb-arr"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="dir"><el-icon><FolderAdd /></el-icon>新建文件夹</el-dropdown-item>
                <el-dropdown-item command="txt"><el-icon><DocumentAdd /></el-icon>新建文本文件</el-dropdown-item>
                <el-dropdown-item command="file"><el-icon><Document /></el-icon>新建空文件</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <el-tooltip content="按文件内容递归搜索" placement="bottom" :hide-after="0">
            <el-button size="small" type="info" @click="openSearch">
              <el-icon><Search /></el-icon>
            </el-button>
          </el-tooltip>
          <!-- 收藏夹：目录右键「收藏」或收藏当前目录，点条目直达（每服务器独立记忆） -->
          <el-popover v-model:visible="favOpen" trigger="click" placement="bottom-start" :width="280">
            <template #reference>
              <el-button size="small" type="warning">
                <el-icon><Star /></el-icon><el-icon class="tb-arr"><ArrowDown /></el-icon>
              </el-button>
            </template>
            <div class="fav-cur" @click="toggleFav(path)">
              <el-icon><StarFilled v-if="isFav(path)" /><Star v-else /></el-icon>
              {{ isFav(path) ? '取消收藏当前目录' : '收藏当前目录' }}
            </div>
            <template v-if="curFavs.length">
              <div class="fav-sep"></div>
              <div class="fav-scroll">
                <div v-for="f in curFavs" :key="f.path" class="fav-item" @click="goFav(f)">
                  <el-icon class="fav-ico"><FolderOpened /></el-icon>
                  <span class="fav-name" :title="f.path">{{ f.name }}</span>
                  <el-icon class="fav-del" title="取消收藏" @click.stop="removeFav(f)"><Close /></el-icon>
                </div>
              </div>
            </template>
            <div v-else class="fav-empty">暂无收藏，可右键目录「收藏」</div>
          </el-popover>
          <el-tooltip content="在终端页打开此服务器" placement="bottom" :hide-after="0">
            <el-button size="small" type="primary" @click="openTerm">
              <el-icon><Platform /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="sel.length ? `复制选中 ${sel.length} 项（Ctrl/Cmd+C）` : '复制（先勾选）'" placement="bottom" :hide-after="0">
            <el-button size="small" type="primary" :disabled="!sel.length" @click="clipAddRows('copy', sel)">
              <el-icon><CopyDocument /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="cur?.clip ? `粘贴（${cur.clip.items.length} 项）` : '剪贴板为空，先复制/剪切'" placement="bottom" :hide-after="0">
            <el-button size="small" type="primary" :disabled="!cur?.clip" @click="pasteClip">
              <el-icon><DocumentCopy /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="sel.length ? `打包下载选中 ${sel.length} 项（tar.gz）` : '勾选后打包下载（tar.gz）'" placement="bottom" :hide-after="0">
            <el-button size="small" type="warning" :disabled="!sel.length" @click="downloadArchive(sel)">
              <el-icon><Download /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="sel.length ? `删除选中 ${sel.length} 项` : '删除（先勾选）'" placement="bottom" :hide-after="0">
            <el-button size="small" type="danger" :disabled="!sel.length" @click="batchRemove()">
              <el-icon><Delete /></el-icon>
            </el-button>
          </el-tooltip>
          <div class="tb-more">
            <el-tooltip v-if="cur && taskDlg.sid === cur.id && taskDlg.status === 'running'" content="查看后台任务进度" placement="bottom" :hide-after="0">
              <el-button size="small" type="warning" @click="reopenTask">
                <el-icon class="is-loading"><Loading /></el-icon>
                <span class="tb-task-pct">{{ taskPct }}%</span>
              </el-button>
            </el-tooltip>
          </div>
          <!-- 名称搜索（自导航行下移）：不勾「包含子目录」= 当前目录即时过滤；
               勾上后回车/点按钮 = 递归搜索，结果进表格（带相对路径，可进入/编辑/操作），
               与「内容搜索」（按文件内容）互补 -->
          <div class="tb-find">
            <el-input
              v-model="filter"
              :placeholder="findSub ? '搜索文件/目录（回车搜子目录）' : '过滤当前目录'"
              size="small"
              clearable
              :prefix-icon="Search"
              class="find-input"
              @keyup.enter="doFind"
              @clear="clearFind"
            />
            <el-checkbox v-model="findSub" class="find-sub" size="small">包含子目录</el-checkbox>
            <el-button size="small" type="primary" :loading="finding" @click="doFind">
              <el-icon v-if="!finding"><Search /></el-icon>
            </el-button>
          </div>
        </div>

        <!-- 子目录搜索结果条：提示搜索词与数量，可清除返回目录列表 -->
        <div v-if="cur.find" class="find-bar">
          <el-icon class="fb-ico"><Search /></el-icon>
          <span class="fb-txt">
            「{{ cur.find.query }}」{{ cur.find.files.length }} 项结果{{ cur.find.truncated ? '（达上限，结果可能不全）' : '' }}
            <span class="fb-dir">· 搜索自 {{ cur.find.root }}</span>
          </span>
          <el-button link size="small" type="danger" class="fb-clear" @click="clearFind">清除返回</el-button>
        </div>

        <!-- 文件表格：单击选中、Shift 范围多选、Ctrl+C/X/V 剪贴板（根元素 tabindex 接收键盘） -->
        <el-table
          ref="tableWrapEl"
          :data="shownFiles"
          v-loading="loading"
          size="small"
          class="fm-table"
          height="100%"
          :empty-text="cur?.find ? '没有匹配的文件/目录' : '空目录'"
          tabindex="0"
          :row-class-name="rowCls"
          @selection-change="(v) => (sel = v)"
          @row-click="onRowClick"
          @row-dblclick="onDblClick"
          @row-contextmenu="onRowCtx"
          @contextmenu="onBlankCtx"
          @keydown="onTableKeydown"
        >
          <el-table-column type="selection" width="42" />
          <el-table-column label="名称" :min-width="colW.name" sortable :sort-method="sortByName" class-name="name-col">
            <template #default="{ row }">
              <el-icon class="f-icon" :style="{ color: fileKind(row).color }">
                <component :is="fileKind(row).icon" />
              </el-icon>
              <!-- 搜索结果模式展示相对路径（悬停看绝对路径），普通模式展示名称 -->
              <span class="f-name" :title="cur?.find ? row.abs : row.name">{{ cur?.find ? row.rel : row.name }}</span>
            </template>
          </el-table-column>
          <el-table-column label="大小" :width="colW.size" sortable sort-by="size">
            <template #default="{ row }">{{ row.is_dir ? '-' : fmtBytes(row.size) }}</template>
          </el-table-column>
          <el-table-column label="类型" :width="colW.kind" sortable :sort-method="sortByKind">
            <template #default="{ row }">{{ fileKind(row).label }}</template>
          </el-table-column>
          <el-table-column label="权限" :width="colW.perm" sortable :sort-method="sortByOwnerPerm">
            <template #default="{ row }">{{ row.perm || '-' }}/{{ row.owner || '-' }}</template>
          </el-table-column>
          <el-table-column label="修改时间" :width="colW.mtime" sortable sort-by="mtime">
            <template #default="{ row }"><span class="f-time">{{ fmtTime(row.mtime) }}</span></template>
          </el-table-column>
          <el-table-column label="操作" :width="colW.ops" align="right" class-name="ops-col">
            <template #default="{ row }">
              <el-tooltip v-if="!row.is_dir" content="下载" placement="top" :hide-after="0">
                <el-button link type="primary" size="small" @click.stop="download(row)">
                  <el-icon><Download /></el-icon>
                </el-button>
              </el-tooltip>
              <el-tooltip v-if="!row.is_dir" content="编辑" placement="top" :hide-after="0">
                <el-button link type="primary" size="small" @click.stop="openEdit(row, rowAbs(row))">
                  <el-icon><EditPen /></el-icon>
                </el-button>
              </el-tooltip>
              <el-tooltip content="重命名" placement="top" :hide-after="0">
                <el-button link size="small" @click.stop="rename(row)">
                  <el-icon><Edit /></el-icon>
                </el-button>
              </el-tooltip>
              <el-tooltip content="删除" placement="top" :hide-after="0">
                <el-button link type="danger" size="small" @click.stop="removeOne(row)">
                  <el-icon><Delete /></el-icon>
                </el-button>
              </el-tooltip>
            </template>
          </el-table-column>
        </el-table>

        <!-- 状态栏：左侧统计 + 右侧根分区磁盘进度条 -->
        <div class="fm-status">
          <template v-if="cur.find">
            <span>搜索「{{ cur.find.query }}」共 {{ shownFiles.length }} 项</span>
          </template>
          <template v-else>
            <span>共 {{ shownFiles.length }} 项{{ filter ? `（目录共 ${files.length} 项）` : '' }}</span>
          </template>
          <span v-if="sel.length">· 已选 {{ sel.length }} 项</span>
          <span class="st-path">· {{ path }}</span>
          <span class="st-srv">· {{ cur.name }}</span>
          <span v-if="cur.disk" class="st-disk">
            <span class="st-disk-txt">磁盘 {{ fmtBytes(cur.disk.used) }} / {{ fmtBytes(cur.disk.total) }}（{{ diskPct }}%）</span>
            <el-progress
              class="st-disk-bar"
              :percentage="diskPct"
              :stroke-width="8"
              :show-text="false"
              :color="barColor(diskPct)"
            />
          </span>
        </div>
      </template>
      <div v-else class="fm-empty">
        <el-icon><FolderOpened /></el-icon>
        <p>点击左侧服务器打开文件管理</p>
        <p class="sub">除「Agent 上报」外的服务器均可连接（自动取配置地址的 22 端口）</p>
        <p class="relay-warn">
          文件上传 / 下载均通过监控服务端中转（浏览器 ↔ 服务端 ↔ 目标机）。<br />
          使用云主机部署请关注流量消耗，建议仅进行小文件操作。
        </p>
      </div>
    </div>

    <!-- 右键菜单（分组）：全屏透明层接管关闭；空白处=粘贴/刷新/新建/搜索 -->
    <Teleport to="body">
      <div v-if="ctxMenu.show" class="ctx-backdrop" @click="ctxMenu.show = false" @contextmenu.prevent />
      <div v-if="ctxMenu.show" class="ctx-menu" :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }">
        <template v-for="(grp, gi) in ctxGroups" :key="gi">
          <div v-if="gi" class="ctx-sep"></div>
          <div
            v-for="it in grp"
            :key="it.key"
            class="ctx-item"
            :class="{ disabled: it.disabled, danger: it.danger }"
            @click="!it.disabled && ctxDo(it.key)"
          >
            <el-icon><component :is="it.icon" /></el-icon>{{ it.label }}
          </div>
        </template>
      </div>
    </Teleport>

    <!-- 在线编辑（Monaco：语法高亮/错误标记/格式化/Ctrl+F 查找，≤5MB） -->
    <el-dialog
      v-model="editDlg.show"
      :title="`编辑 - ${editDlg.name}`"
      width="min(94vw, 1080px)"
      destroy-on-close
      append-to-body
      :before-close="editGuard.beforeClose"
    >
      <div class="ed-bar">
        <el-select v-model="editDlg.lang" size="small" filterable class="ed-lang">
          <el-option v-for="l in LANGS" :key="l.v" :label="l.l" :value="l.v" />
        </el-select>
        <el-button size="small" @click="formatEdit">格式化</el-button>
        <el-button size="small" @click="findInEdit">搜索内容</el-button>
        <span class="ed-tip">Ctrl/Cmd + S 保存 · Ctrl/Cmd + F 查找</span>
      </div>
      <div class="ed-body" @keydown.capture="onEditKeydown">
        <!-- 加载失败按 HTML 规范模块图缓存失败结果，页内重试同一地址永远无效，
             故给「刷新重试」而非原地重试；简易编辑器兜底保证手机端仍可查看/保存 -->
        <div v-if="edPlain" class="ed-plain">
          <el-input v-model="editDlg.content" type="textarea" resize="none" class="ed-plain-ta" />
          <span class="ed-plain-tip">简易编辑模式（无语法高亮），保存不受影响</span>
        </div>
        <div v-else-if="edErr" class="ed-chunk-tip">
          <p>编辑器组件加载失败（网络不稳定或系统更新后缓存失效）</p>
          <div class="ed-chunk-btns">
            <el-button size="small" @click="reloadForEd">刷新页面重试</el-button>
            <el-button size="small" type="primary" @click="edPlain = true">使用简易编辑器</el-button>
          </div>
        </div>
        <component :is="edComp" v-else-if="edComp" ref="codeEd" v-model="editDlg.content" :language="editDlg.lang" :loading="editDlg.loading" class="ed-code" />
        <div v-else class="ed-chunk-tip">
          <el-icon class="is-loading ed-chunk-spin"><Loading /></el-icon>
          <p>编辑器组件加载中（约 4MB，弱网下首次打开稍慢）…</p>
        </div>
      </div>
      <template #footer>
        <span class="edit-tip">语法可切换；保存后生效</span>
        <el-button @click="editDlg.show = false">关闭</el-button>
        <el-button type="primary" :loading="editDlg.saving" @click="saveEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 图片预览：双击图片文件直接弹层显示（下载通道同源带 HttpOnly Cookie 鉴权），
         点击图片全屏放大 -->
    <el-dialog v-model="imgDlg.show" :title="`图片 - ${imgDlg.name}`" width="min(94vw, 1080px)" append-to-body>
      <div class="img-view">
        <el-image :src="imgDlg.url" :preview-src-list="[imgDlg.url]" hide-on-click-modal preview-teleported>
          <template #error>
            <div class="img-err">图片加载失败（文件损坏或格式不支持）</div>
          </template>
        </el-image>
      </div>
    </el-dialog>

    <!-- 属性（「属性」基本信息 + 「权限」两个选项卡；目录打开即后台递归统计，实时回传） -->
    <el-dialog v-model="propsDlg.show" :title="`属性 - ${propsDlg.name}`" width="640px" append-to-body @close="stopPropsCount">
      <el-tabs v-model="propsDlg.tab">
        <el-tab-pane label="属性" name="info">
          <div class="pp-grid">
            <span class="pp-k">类型</span>
            <span>{{ propsDlg.isDir ? '目录' : (propsDlg.mode || '').startsWith('l') ? '链接' : '文件' }}</span>
            <span class="pp-k">位置</span>
            <span class="pp-v">
              <span class="pp-path" :title="propsDlg.path">{{ propsDlg.path }}</span>
              <el-icon class="pp-copy" title="复制路径" @click="copyPropsPath"><CopyDocument /></el-icon>
            </span>
            <span class="pp-k">大小</span>
            <span>
              <template v-if="propsDlg.isDir">
                <template v-if="propsDlg.counted">{{ fmtBytes(propsDlg.rsize) + (propsDlg.calcDone ? '（递归）' : '（统计中…）') }}</template>
                <template v-else>{{ propsDlg.counting ? '统计中…' : '未计算' }}</template>
                <el-button v-if="!propsDlg.calcDone && !propsDlg.counting" size="small" class="pp-calc" @click="startPropsCount">计算</el-button>
              </template>
              <template v-else>{{ fmtBytes(propsDlg.size) }}</template>
            </span>
            <span class="pp-k">修改时间</span>
            <span>{{ fmtTime(propsDlg.mtime) }}</span>
            <span class="pp-k">访问时间</span>
            <span>{{ fmtTime(propsDlg.atime) }}</span>
            <template v-if="propsDlg.isDir">
              <span class="pp-k">包含</span>
              <span>
                <span v-if="propsDlg.counted">
                  {{ propsDlg.files }} 个文件、{{ propsDlg.dirs }} 个文件夹
                  <span v-if="propsDlg.skipped" class="pp-wait">（{{ propsDlg.skipped }} 个子目录无权限，未计入）</span>
                </span>
                <span v-else class="pp-wait">{{ propsDlg.counting ? '统计中…' : '未统计' }}</span>
              </span>
            </template>
          </div>
        </el-tab-pane>
        <el-tab-pane label="权限" name="perm">
          <div class="perm-boxes">
            <div v-for="g in permShifts" :key="g.shift" class="perm-box">
              <div class="perm-box-title">{{ g.label }}</div>
              <el-checkbox :model-value="hasPerm(g.shift, 4)" @change="togglePerm(g.shift, 4)">读取</el-checkbox>
              <el-checkbox :model-value="hasPerm(g.shift, 2)" @change="togglePerm(g.shift, 2)">写入</el-checkbox>
              <el-checkbox :model-value="hasPerm(g.shift, 1)" @change="togglePerm(g.shift, 1)">执行</el-checkbox>
            </div>
          </div>
          <div class="perm-foot">
            <el-input v-model="permText" size="small" class="perm-num" />
            <span class="perm-hint">权限，所有者</span>
            <el-select v-model="propsDlg.owner" size="small" filterable class="perm-owner" placeholder="保持不变">
              <el-option v-for="u in permOwnerOpts" :key="u" :label="u" :value="u" />
            </el-select>
            <el-checkbox v-if="propsDlg.isDir" v-model="propsDlg.recursive">应用到子目录</el-checkbox>
            <el-button type="success" size="small" class="perm-apply" @click="applyPropsPerm">应用</el-button>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-dialog>

    <!-- 后台任务进度（创建压缩包/解压）：完成自动关闭，失败停留展示原因 -->
    <el-dialog v-model="taskDlg.show" :title="taskDlg.title" width="420px" append-to-body :close-on-click-modal="false">
      <template v-if="taskDlg.status !== 'error'">
        <el-progress :percentage="taskPct" :stroke-width="12" :color="barColorBlue()" />
        <div class="task-sub">{{ taskSubText }}</div>
      </template>
      <div v-else class="task-err">{{ taskDlg.error }}</div>
      <template #footer>
        <el-button v-if="taskDlg.status === 'error'" type="primary" @click="taskDlg.show = false">知道了</el-button>
        <template v-else>
          <el-button type="danger" plain :loading="taskDlg.canceling" @click="cancelTask">取消任务</el-button>
          <el-button @click="taskDlg.show = false">后台运行</el-button>
        </template>
      </template>
    </el-dialog>

    <!-- 文件内容搜索（可递归子目录，按后缀过滤；命中文件 + 命中行预览） -->
    <el-dialog v-model="searchDlg.show" title="搜索文件内容" width="min(94vw, 880px)" append-to-body>
      <div class="s-form">
        <div class="s-row">
          <span class="s-lab">搜索内容</span>
          <el-input
            v-model="searchDlg.query"
            placeholder="关键字或正则表达式"
            class="s-query"
            @keyup.enter="doSearch"
          />
          <el-checkbox v-model="searchDlg.cs">区分大小写</el-checkbox>
          <el-checkbox v-model="searchDlg.regex">正则</el-checkbox>
        </div>
        <div class="s-row">
          <span class="s-lab">目录</span>
          <el-input v-model="searchDlg.dir" class="s-query" @keyup.enter="doSearch" />
          <el-checkbox v-model="searchDlg.recursive">包含子目录</el-checkbox>
        </div>
        <div class="s-row">
          <span class="s-lab">文件后缀</span>
          <el-input v-model="searchDlg.ext" placeholder="如 log,txt,conf（留空=全部文本文件）" class="s-query" @keyup.enter="doSearch" />
          <el-button type="primary" :loading="searchDlg.busy" @click="doSearch">搜索</el-button>
        </div>
      </div>
      <template v-if="searchDlg.files">
        <div class="s-head">
          <span>{{ searchSummary }}</span>
          <el-button link size="small" type="danger" @click="searchDlg.files = null">取消搜索</el-button>
        </div>
        <div class="s-body">
          <div class="s-files">
            <div
              v-for="f in searchDlg.files"
              :key="f.path"
              class="s-file"
              :class="{ on: searchDlg.cur?.path === f.path }"
              @click="searchDlg.cur = f"
            >
              <span class="s-fname" :title="f.path">{{ f.name }}</span>
              <el-tag size="small" type="danger" effect="plain">{{ f.count }}</el-tag>
            </div>
            <div v-if="!searchDlg.files.length" class="s-none">没有匹配的文件</div>
          </div>
          <div class="s-lines">
            <template v-if="searchDlg.cur">
              <div class="s-lhead">
                <span class="s-lpath" :title="searchDlg.cur.path">{{ searchDlg.cur.path }}</span>
                <el-button link type="primary" size="small" @click="editFromSearch(searchDlg.cur)">编辑</el-button>
              </div>
              <div v-for="l in searchDlg.cur.lines" :key="l.no" class="s-line">
                <span class="s-no">行{{ l.no }}：</span>
                <!-- 内容经 escapeHtml 转义后再拼 <mark>，无注入风险 -->
                <span class="s-txt" v-html="hlLine(l.text)"></span>
              </div>
            </template>
            <div v-else class="s-none">左侧选择文件查看命中行</div>
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- 传输面板：右下角浮层（上传任务；下载由浏览器下载器管理，不进面板）。
         teleport 到 body：violet 玻璃下 .main 的 backdrop-filter 会成为 fixed 的
         containing block，不逃出去就锚在内容区底部而非视口右下角（463） -->
    <Teleport to="body">
      <div v-if="transfers.length" class="xfers">
      <div class="xfers-head">
        <span>上传列表<template v-if="activeCount">（{{ activeCount }} 个进行中）</template></span>
        <el-button link size="small" @click="clearDone">清除记录</el-button>
      </div>
      <div class="xfers-list">
        <div v-for="tr in transfers" :key="tr.key" class="xfer">
          <el-icon class="x-ico"><UploadFilled /></el-icon>
          <div class="x-main">
            <div class="x-name" :title="tr.name">{{ tr.name }}</div>
            <el-progress
              :percentage="tr.pct"
              :stroke-width="5"
              :show-text="false"
              :color="barColorBlue()"
              :status="tr.status === 'success' ? 'success' : tr.status === 'exception' ? 'exception' : undefined"
            />
            <div class="x-sub">
              <span :title="tr.status === 'exception' ? tr.statusText : ''">{{ xferSub(tr) }}</span>
              <span v-if="tr.status === 'active'" class="x-cancel" @click="cancelXfer(tr)">取消</span>
              <span v-else-if="tr.status === 'canceled' || tr.status === 'exception'" class="x-cancel" @click="resumeXfer(tr)">续传</span>
            </div>
          </div>
        </div>
      </div>
    </div>
    </Teleport>

    <SshCredDialog ref="credDlg" />
  </div>
</template>

<script setup>
// 独立文件管理页（SFTP）：左栏分组服务器 + 多标签（每台服务器一个标签，
// 各标签独立记忆路径/历史/过滤/剪贴板）+ 宝塔风格文件区（后退+路径框/图标
// 工具栏/表格多选/分组右键菜单/状态栏磁盘条）。SFTP 连接按 serverID 池化
// 在服务端；连接失败弹 SshCredDialog 试连后重试。上传串行可取消/续传；
// 下载/打包下载走浏览器原生下载器。压缩/解压走服务端后台任务（进度轮询，
// 完毕自动关窗）；在线编辑用 Monaco（语法高亮/JSON 错误标记/格式化/查找）；
// 内容搜索递归子目录、命中行预览。列宽可拖拽；属性弹窗合并权限修改。
defineOptions({ name: 'FilesView' })
import { computed, markRaw, nextTick, onActivated, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import {
  ArrowDown, Back, Close, CopyDocument, Delete, Document, DocumentAdd, DocumentCopy, Download,
  Edit, EditPen, FolderAdd, FolderOpened, Loading, Platform, Refresh, Search, Star, StarFilled,
  Top, Upload, UploadFilled,
} from '@element-plus/icons-vue'
import { api, uploadFileChunked, BASE } from '../api/index.js'
import { setTermPending } from '../termPending.js'
import { barColor, barColorBlue } from '../utils/chartTheme.js'
import { fmtBytes, fmtTime } from '../utils/format.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useSidePanel } from '../composables/useSidePanel.js'
import SshCredDialog from '../components/SshCredDialog.vue'
import ServerTree from '../components/ServerTree.vue'

// Monaco 编辑器独立 chunk（本体约 4MB）：首次打开编辑弹窗才拉取。手动管理加载态
// ——加载中显示提示、失败给重试按钮。此前用 defineAsyncComponent 无任何反馈，
// 弱网加载慢只见纯白，失败则永久空白（手机端打开文件编辑器一片白的根因：
// 连编辑器容器边框都没有 = 组件根本没挂载）
const edComp = shallowRef(null)
const edErr = ref(false)
let edBusy = false
async function loadEdChunk() {
  if (edComp.value || edBusy) return
  edBusy = true
  edErr.value = false
  try {
    const m = await import('../components/CodeEditor.vue')
    edComp.value = m.default
  } catch {
    edErr.value = true
  } finally {
    edBusy = false
  }
}
const edPlain = ref(false)
function reloadForEd() {
  location.reload()
}

const servers = ref([])
const groups = ref([])
// 每标签独立：path/history(files+hidx)/files/sel/filter/clip/disk
const tabs = ref([])
const activeID = ref(0)
const credDlg = ref(null)
const upInput = ref(null)
const ctxMenu = ref({ show: false, x: 0, y: 0, row: null })
const isMobile = useIsMobile()
const side = useSidePanel('files', 210) // 左栏宽度/收起，localStorage 记忆

// 当前标签与各状态的计算属性代理：业务函数沿用单值写法，实际读写落在
// activeID 对应的标签对象上（每台服务器独立记忆）
const cur = computed(() => tabs.value.find((t) => t.id === activeID.value) || null)
const path = computed({
  get: () => cur.value?.path ?? '/',
  set: (v) => { if (cur.value) cur.value.path = v },
})
const files = computed({
  get: () => cur.value?.files ?? [],
  set: (v) => { if (cur.value) cur.value.files = v },
})
const sel = computed({
  get: () => cur.value?.sel ?? [],
  set: (v) => { if (cur.value) cur.value.sel = v },
})
const filter = computed({
  get: () => cur.value?.filter ?? '',
  set: (v) => { if (cur.value) cur.value.filter = v },
})
const loading = computed({
  get: () => !!cur.value?.loading,
  set: (v) => { if (cur.value) cur.value.loading = v },
})
const diskPct = computed(() => {
  const d = cur.value?.disk
  if (!d || !d.total) return 0
  return Math.min(100, Math.round((d.used / d.total) * 100))
})

function tabOf(id) {
  return tabs.value.find((t) => t.id === id)
}

// ---------- 标签页 ----------

async function openTab(s) {
  if (!s) return
  let t = tabOf(s.id)
  if (!t) {
    t = reactive({ id: s.id, name: s.name, path: '/', files: [], sel: [], filter: '', loading: false, clip: null, disk: null, find: null, hist: ['/'], hidx: 0 })
    tabs.value.push(t)
    activeID.value = s.id
    if (favs.value[s.id] === undefined) loadFavs(s.id)
    await list()
    return
  }
  activeID.value = s.id
  if (favs.value[s.id] === undefined) loadFavs(s.id)
}

function closeTab(t) {
  const i = tabs.value.indexOf(t)
  if (i < 0) return
  tabs.value.splice(i, 1)
  if (activeID.value === t.id) {
    const next = tabs.value[Math.min(i, tabs.value.length - 1)]
    activeID.value = next ? next.id : 0
  }
}

watch(activeID, () => {
  ctxMenu.value.show = false
  pathEditing.value = false
  pathInput.value = path.value
})

// ---------- 连接与列表 ----------

function credKind(s) {
  if (!s.ssh_user) return 'both'
  if (s.ssh_password_set || s.ssh_key_path) return 'none'
  return 'pass'
}

async function verifyCred(cred) {
  // push 机器弹窗补填的 SSH 地址先落库（383），试连与后续连接才有地址可用
  const s = servers.value.find((x) => x.id === cur.value?.id)
  if (cred.sshAddr && s) {
    await api.put(`/servers/${s.id}`, { ssh_addr: cred.sshAddr })
    s.ssh_addr = cred.sshAddr
  }
  await api.post('/term/sftp-auth', {
    server_id: cur.value.id,
    username: cred.username,
    password: cred.password,
    save: cred.save,
  })
}

async function list() {
  const t = cur.value
  if (!t) return
  t.loading = true
  try {
    const res = await api.get(`/term/files?server_id=${t.id}&path=${encodeURIComponent(t.path)}`)
    t.files = markRaw(res.files || []) // 大目录条目只读展示，跳过深度响应代理
    t.path = res.path || t.path
    t.disk = res.disk || null
    if (!pathEditing.value) pathInput.value = t.path
  } catch (e) {
    t.files = []
    if (/SSH 连接失败/.test(e.message)) {
      // 凭据缺失/失效：弹框试连，成功后自动重试列表
      const s = servers.value.find((x) => x.id === t.id) || t
      let kind = credKind(s)
      if (kind === 'none') kind = 'pass'
      const cred = await credDlg.value.open(s, kind, { verify: verifyCred, error: e.message })
      if (cred) await list()
    } else {
      ElMessage.error(e.message)
    }
  } finally {
    t.loading = false
    nextTick(bindColResize)
  }
}

// ---------- 导航 ----------

const shownFiles = computed(() => {
  const t = cur.value
  if (!t) return []
  // 搜索结果模式：表格直接展示结果（不再叠加本地过滤，清除搜索即回目录列表）
  if (t.find) return t.find.files
  const k = t.filter.trim().toLowerCase()
  return k ? t.files.filter((f) => f.name.toLowerCase().includes(k)) : t.files
})

// ---------- 名称搜索（可含子目录）：结果进表格，条目带绝对路径 ----------

const findSub = ref(false) // 「包含子目录」勾选（全局偏好，不按标签记忆）
const finding = ref(false)

// 行条目的绝对路径：搜索结果模式行自带 abs；普通模式按当前目录拼
function rowAbs(row) {
  return cur.value?.find ? row.abs : join(row.name)
}

async function doFind() {
  const t = cur.value
  if (!t) return
  const q = t.filter.trim()
  if (!q) {
    ElMessage.warning('请输入要搜索的文件/目录名称')
    return
  }
  if (!findSub.value) {
    // 未勾「包含子目录」：当前目录即时过滤即是结果，不发包
    clearFind()
    return
  }
  finding.value = true
  t.loading = true
  try {
    const root = t.path
    const r = await api.post(`/term/files/find?server_id=${t.id}`, { path: root, query: q })
    // 行的 name 取末段（图标/类型/排序/编辑标题用），rel 为相对搜索根的展示路径，abs 备操作用
    const list = (r.files || []).map((f) => ({
      ...f,
      name: f.path.split('/').pop() || f.path,
      rel: f.path === root ? f.path : f.path.slice(root.length).replace(/^\//, '') || f.path,
      abs: f.path,
    }))
    t.find = { root, query: q, files: list, truncated: !!r.truncated }
    t.sel = []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    finding.value = false
    t.loading = false
  }
}

function clearFind() {
  const t = cur.value
  if (t?.find) {
    t.find = null
    t.sel = []
  }
}

// 操作后的视图刷新：搜索结果模式重跑搜索（结果里刚删/改的条目要消失），否则刷目录
function refreshView() {
  if (cur.value?.find) doFind()
  else list()
}

// ---------- 工具栏下拉/跳转 ----------

const router = useRouter()

function onCreate(cmd) {
  if (cmd === 'dir') newDir()
  else newFile(cmd === 'txt')
}

// 跳终端页并打开当前服务器会话（意图经 termPending 传递，TermView 载入后取走）
function openTerm() {
  const t = cur.value
  if (!t) return
  const s = servers.value.find((x) => x.id === t.id)
  if (s) setTermPending(s)
  router.push('/console/term')
}

const crumbSegs = computed(() => {
  let acc = ''
  return path.value.split('/').filter(Boolean).map((p) => {
    acc += '/' + p
    return { name: p, path: acc }
  })
})

// 路径框：常态显示面包屑（可点击切换），聚焦变为可输入的文本框，回车跳转
const pathInput = ref('/')
const pathEditing = ref(false)
const pathInputEl = ref(null)

function onPathBlur() {
  pathEditing.value = false
  pathInput.value = path.value
}

function goPathInput() {
  const p = pathInput.value.trim()
  if (!p.startsWith('/')) {
    ElMessage.warning('路径必须是绝对路径（以 / 开头）')
    return
  }
  pathInputEl.value?.blur()
  go(p)
}

async function copyText(s) {
  try {
    await navigator.clipboard.writeText(s)
    ElMessage.success('已复制')
  } catch {
    // 非 HTTPS 环境没有 clipboard API：回落 execCommand
    const ta = document.createElement('textarea')
    ta.value = s
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
      ElMessage.success('已复制')
    } catch {
      ElMessage.error('复制失败')
    }
    ta.remove()
  }
}

function copyPath() {
  copyText(path.value)
}

function join(name) {
  return path.value === '/' ? '/' + name : path.value + '/' + name
}

function joinOn(dir, name) {
  return dir === '/' ? '/' + name : dir + '/' + name
}

// go 记录历史（后退按钮用）；截断「后退后又前进」的分支。导航一律退出搜索结果态
function go(p) {
  const t = cur.value
  if (!t) return
  clearFind()
  if (p === path.value) {
    list()
    return
  }
  t.hist = t.hist.slice(0, t.hidx + 1)
  t.hist.push(p)
  if (t.hist.length > 50) t.hist.shift()
  t.hidx = t.hist.length - 1
  path.value = p
  sel.value = []
  list()
}

function goBack() {
  const t = cur.value
  if (!t || t.hidx <= 0) return
  clearFind()
  t.hidx--
  path.value = t.hist[t.hidx]
  sel.value = []
  list()
}

// '/a/b' → '/a'，'/a' → '/'，'/' → '/'
function parentPath(p) {
  if (p === '/') return '/'
  const i = p.replace(/\/+$/, '').lastIndexOf('/')
  return i <= 0 ? '/' : p.slice(0, i)
}

function goUp() {
  const p = parentPath(path.value)
  if (p !== path.value) go(p)
}

// ---------- 收藏夹（每服务器独立，存服务端跨设备同步；点条目直达） ----------

const FAV_KEY = 'lz.fm.favs' // 旧版本地收藏（仅迁移用），逐台迁移完成后自动清除
const favs = ref({})
const favOpen = ref(false)
const legacyFavs = safeParseFavs()

function safeParseFavs() {
  try {
    return JSON.parse(localStorage.getItem(FAV_KEY)) || {}
  } catch {
    return {}
  }
}

function persistLegacy() {
  if (Object.keys(legacyFavs).length) localStorage.setItem(FAV_KEY, JSON.stringify(legacyFavs))
  else localStorage.removeItem(FAV_KEY)
}

// 保存到服务端（失败静默：本地状态保留，下次操作再整份重传）
function saveFavs(sid, items) {
  api.put('/term/favs', { server_id: sid, items }).catch(() => {})
}

// 打开/切换服务器标签时拉取该服务器收藏；旧版本地收藏（只存当时那台设备，
// 手机上恒为空）合并去重后回传服务端一次，各设备自此同源
async function loadFavs(sid) {
  try {
    const r = await api.get(`/term/favs?server_id=${sid}`)
    let items = (r.items || []).map((f) => ({ path: f.path, name: f.name || favName(f.path) }))
    const legacy = legacyFavs[sid]
    if (legacy?.length) {
      const paths = new Set(items.map((f) => f.path))
      items = items.concat(
        legacy.filter((f) => f.path && !paths.has(f.path)).map((f) => ({ path: f.path, name: f.name || favName(f.path) })),
      )
      delete legacyFavs[sid]
      persistLegacy()
      saveFavs(sid, items)
    }
    favs.value[sid] = items
  } catch {
    /* 拉取失败回落旧版本地数据（可能为空），不阻塞文件列表 */
    favs.value[sid] = legacyFavs[sid] || []
  }
}

const curFavs = computed(() => favs.value[cur.value?.id] || [])

function favName(p) {
  if (p === '/') return '/'
  return p.replace(/\/+$/, '').split('/').pop() || p
}

function isFav(p) {
  return curFavs.value.some((f) => f.path === p)
}

function toggleFav(p) {
  const t = cur.value
  if (!t) return
  if (!favs.value[t.id]) favs.value[t.id] = []
  const list = favs.value[t.id]
  const i = list.findIndex((f) => f.path === p)
  const adding = i < 0
  if (adding) list.push({ path: p, name: favName(p) })
  else list.splice(i, 1)
  saveFavs(t.id, list)
  ElMessage.success(adding ? '已收藏' : '已取消收藏')
}

function removeFav(f) {
  const t = cur.value
  const list = favs.value[t?.id]
  if (!list) return
  const i = list.indexOf(f)
  if (i >= 0) list.splice(i, 1)
  saveFavs(t.id, list)
}

// 直达收藏目录（记录进后退历史，与手动跳转同链路）
function goFav(f) {
  favOpen.value = false
  go(f.path)
}

function sortByName(a, b) {
  if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1 // 目录在前（与服务端排序一致）
  return a.name.localeCompare(b.name, 'zh', { numeric: true })
}

// ---------- 文件类型分类（名称列图标 + 类型列共用） ----------

// 扩展名 → 类型键（常规文件）；目录/链接/设备看 mode 首字符
const KIND_BY_EXT = {
  png: 'img', jpg: 'img', jpeg: 'img', gif: 'img', bmp: 'img', svg: 'img', webp: 'img', ico: 'img',
  mp4: 'video', mkv: 'video', avi: 'video', mov: 'video', webm: 'video', flv: 'video', m4v: 'video',
  mp3: 'audio', flac: 'audio', wav: 'audio', ogg: 'audio', m4a: 'audio', aac: 'audio',
  zip: 'arch', tar: 'arch', gz: 'arch', bz2: 'arch', xz: 'arch', '7z': 'arch', rar: 'arch', zst: 'arch', tgz: 'arch',
  go: 'code', py: 'code', js: 'code', ts: 'code', sh: 'code', bash: 'code', php: 'code', java: 'code',
  c: 'code', cpp: 'code', h: 'code', rs: 'code', rb: 'code', pl: 'code', lua: 'code', sql: 'code',
  yaml: 'code', yml: 'code', json: 'code', toml: 'code', ini: 'code', conf: 'code',
}
const DOC_EXTS = new Set(['pdf', 'html', 'htm', 'md', 'txt', 'log', 'csv', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx'])
// 图标名是全局注册的 Element Plus 图标组件，直接用字符串渲染
const KINDS = {
  dir: { label: '目录', icon: 'FolderOpened', color: '#4d8df5' },
  link: { label: '链接', icon: 'Link', color: '#1fb8b8' },
  dev: { label: '设备', icon: 'Cpu', color: '#8a9099' },
  img: { label: '图片', icon: 'Picture', color: '#9d7bf5' },
  video: { label: '视频', icon: 'VideoPlay', color: '#e8738e' },
  audio: { label: '音频', icon: 'Headset', color: '#37b57c' },
  arch: { label: '压缩包', icon: 'Files', color: '#e0a63d' },
  code: { label: '脚本', icon: 'DocumentCopy', color: '#d47a4e' },
  doc: { label: '文档', icon: 'Document', color: '#8a9099' },
  file: { label: '文件', icon: 'Document', color: '#8a9099' },
}

function fileKind(row) {
  if (row.is_dir) return KINDS.dir
  const c = (row.mode || '')[0]
  if (c === 'l') return KINDS.link
  if (c === 'c' || c === 'b' || c === 'p' || c === 's') return KINDS.dev
  const ext = row.name.includes('.') ? row.name.split('.').pop().toLowerCase() : ''
  if (KIND_BY_EXT[ext]) return KINDS[KIND_BY_EXT[ext]]
  if (DOC_EXTS.has(ext)) return KINDS.doc
  return KINDS.file
}

function sortByKind(a, b) {
  const ka = fileKind(a).label
  const kb = fileKind(b).label
  return ka !== kb ? ka.localeCompare(kb, 'zh') : a.name.localeCompare(b.name, 'zh', { numeric: true })
}

// 权限列（数字/所有者）排序：先按所有者再按权限
function sortByOwnerPerm(a, b) {
  const r = (a.owner || '').localeCompare(b.owner || '', 'zh')
  if (r !== 0) return r
  const p = (a.perm || '').localeCompare(b.perm || '')
  return p !== 0 ? p : a.name.localeCompare(b.name, 'zh', { numeric: true })
}

// 双击：目录进入；压缩包询问是否解压到所在目录；图片直接弹层预览；其余文件打开编辑。
// 搜索结果模式下按条目绝对路径操作（rowAbs）
function onDblClick(row) {
  if (row.is_dir) {
    go(rowAbs(row))
    return
  }
  if (isArchiveName(row.name)) {
    ElMessageBox.confirm(`「${row.name}」是压缩包，解压到所在目录？`, '解压确认', {
      type: 'warning',
      confirmButtonText: '解压',
      cancelButtonText: '取消',
    })
      .then(() => startUnarchive(row))
      .catch(() => {})
    return
  }
  if (isImageName(row.name)) {
    previewImage(row)
    return
  }
  openEdit(row, rowAbs(row))
}

// ---------- 图片预览（下载通道同源加载，HttpOnly Cookie 自动随请求） ----------
const IMG_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'ico'])
const imgDlg = reactive({ show: false, name: '', url: '' })

function isImageName(name) {
  const e = name.includes('.') ? name.split('.').pop().toLowerCase() : ''
  return IMG_EXTS.has(e)
}

function previewImage(row) {
  imgDlg.name = row.name
  imgDlg.url = `${BASE}/term/files/download?server_id=${cur.value.id}&path=${encodeURIComponent(rowAbs(row))}`
  imgDlg.show = true
}

// ---------- 点选 / Shift 多选 / 快捷键剪贴板 ----------

const tableWrapEl = ref(null)
const selAnchor = ref('') // shift 范围选择的锚点（上次点击的文件名）

function tableApi() {
  return tableWrapEl.value?.$el ? tableWrapEl.value : null
}

// 单击=只选该行；Ctrl/Cmd=切换；Shift=从锚点到当前行全选。
// 复选框/操作按钮的点击不在此处理（各自有原生行为）
function onRowClick(row, _col, ev) {
  const t = ev?.target
  if (t?.closest?.('.el-checkbox') || t?.closest?.('.el-button')) return
  const tb = tableApi()
  if (!tb) return
  const anchor = selAnchor.value // shift 范围以「上次点击」为起点，先取再更新
  selAnchor.value = row.name
  if (ev.shiftKey) {
    const list = shownFiles.value
    const a = list.findIndex((f) => f.name === anchor)
    const b = list.indexOf(row)
    if (a >= 0 && b >= 0) {
      tb.clearSelection()
      for (let i = Math.min(a, b); i <= Math.max(a, b); i++) tb.toggleRowSelection(list[i], true)
    }
    return
  }
  if (ev.ctrlKey || ev.metaKey) {
    tb.toggleRowSelection(row)
  } else {
    tb.clearSelection()
    tb.toggleRowSelection(row, true)
  }
}

// Ctrl/Cmd+C/X/V：表格区域获得焦点时生效（点过行/表头即有焦点）
function onTableKeydown(e) {
  if (!(e.ctrlKey || e.metaKey)) return
  const k = (e.key || '').toLowerCase()
  if (k === 'c' && sel.value.length) {
    e.preventDefault()
    clipAddRows('copy', sel.value)
  } else if (k === 'x' && sel.value.length) {
    e.preventDefault()
    clipAddRows('cut', sel.value)
  } else if (k === 'v' && cur.value?.clip) {
    e.preventDefault()
    pasteClip()
  }
}

// 勾选行打 sel-row 类：Element 的 current-row 只配合 highlight-current-row
//（单击选中不走那套），选中底色由本类自己刷
function rowCls({ row }) {
  return sel.value.some((r) => r.name === row.name) ? 'sel-row' : ''
}

// ---------- 列宽拖拽（el-table 无原生列宽调整：表头右缘挂把手） ----------

const colW = reactive({ name: 260, size: 100, kind: 76, perm: 130, mtime: 190, ops: 150 })
// 与列定义顺序一致；第 0 列是选择框不做拖拽
const COL_KEYS = ['name', 'size', 'kind', 'perm', 'mtime', 'ops']

function bindColResize() {
  const root = tableWrapEl.value?.$el || tableWrapEl.value
  const ths = root?.querySelectorAll('.el-table__header-wrapper thead th') || []
  ths.forEach((th, i) => {
    const key = COL_KEYS[i - 1]
    if (!key || th.querySelector('.th-drag')) return
    const h = document.createElement('div')
    h.className = 'th-drag'
    h.addEventListener('mousedown', (ev) => startResize(key, ev))
    th.appendChild(h)
  })
}

function startResize(key, ev) {
  ev.preventDefault()
  const startX = ev.clientX
  const startW = colW[key]
  const min = key === 'name' ? 160 : key === 'kind' ? 64 : 60
  const move = (e) => {
    colW[key] = Math.max(min, startW + e.clientX - startX)
  }
  const up = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
}

// ---------- 右键菜单（分组） ----------

function showCtx(ev, row) {
  const mw = 170
  const mh = 420
  ctxMenu.value = {
    show: true,
    x: Math.min(ev.clientX, window.innerWidth - mw - 8),
    y: Math.min(ev.clientY, window.innerHeight - mh - 8),
    row,
  }
}

function onRowCtx(row, _col, ev) {
  ev.preventDefault()
  ev.stopPropagation()
  // 右键即选中（资源管理器语义）：该行不在当前选择里时清掉原选择只选它，
  // 已在多选内则保留整批（右键批量操作照常可用）
  if (!sel.value.some((r) => r.name === row.name)) {
    const tb = tableApi()
    if (tb) {
      tb.clearSelection()
      tb.toggleRowSelection(row, true)
    }
  }
  showCtx(ev, row)
}

// 空白处右键（行内右键已 stopPropagation，到不了这里）
function onBlankCtx(ev) {
  if (!cur.value) return
  ev.preventDefault()
  showCtx(ev, null)
}

// 右键对象若在多选里且选了多项，则整批操作（复制/剪切/打包）
function ctxRows(row) {
  return sel.value.some((r) => r.name === row.name) && sel.value.length > 1 ? sel.value : [row]
}

// 菜单分组：打开（目录）/下载（文件）/编辑 → 复制剪切 → 压缩 → 解压 → 属性 → 重命名删除；
// 空白处：剪贴板/视图 → 新建 → 搜索
const ctxGroups = computed(() => {
  const row = ctxMenu.value.row
  if (!row) {
    return [
      [
        { key: 'paste', icon: 'DocumentCopy', label: '粘贴', disabled: !cur.value?.clip },
        { key: 'refresh', icon: 'Refresh', label: '刷新' },
      ],
      [
        { key: 'mkdir', icon: 'FolderAdd', label: '新建文件夹' },
        { key: 'newtxt', icon: 'DocumentAdd', label: '新建文本文件' },
        { key: 'newfile', icon: 'Document', label: '新建空文件' },
      ],
      [{ key: 'search', icon: 'Search', label: '搜索文件内容' }],
    ]
  }
  const multi = ctxRows(row)
  const groups = [
    [
      row.is_dir
        ? { key: 'open', icon: 'FolderOpened', label: '进入目录' }
        : { key: 'download', icon: 'Download', label: '下载' },
      ...(row.is_dir ? [] : [{ key: 'edit', icon: 'EditPen', label: '编辑' }]),
      ...(row.is_dir
        ? [{ key: 'fav', icon: isFav(rowAbs(row)) ? 'StarFilled' : 'Star', label: isFav(rowAbs(row)) ? '取消收藏' : '收藏' }]
        : []),
    ],
    [
      { key: 'copy', icon: 'CopyDocument', label: multi.length > 1 ? `复制 ${multi.length} 项` : '复制' },
      { key: 'cut', icon: 'Scissor', label: multi.length > 1 ? `剪切 ${multi.length} 项` : '剪切' },
    ],
    [
      { key: 'archive', icon: 'Box', label: multi.length > 1 ? `压缩 ${multi.length} 项` : '创建压缩包' },
      { key: 'archivedl', icon: 'Download', label: '压缩并下载' },
    ],
  ]
  if (!row.is_dir && isArchiveName(row.name)) {
    groups.push([{ key: 'unarchive', icon: 'FolderOpened', label: '解压到当前目录' }])
  }
  groups.push(
    [
      { key: 'rename', icon: 'Edit', label: '重命名' },
      { key: 'remove', icon: 'Delete', label: multi.length > 1 ? `删除 ${multi.length} 项` : '删除', danger: true },
    ],
    [{ key: 'props', icon: 'InfoFilled', label: '属性' }],
  )
  return groups
})

function ctxDo(act) {
  const row = ctxMenu.value.row
  ctxMenu.value.show = false
  switch (act) {
    case 'paste':
      if (cur.value?.clip) pasteClip()
      return
    case 'refresh':
      list()
      return
    case 'search':
      openSearch()
      return
    case 'mkdir':
      newDir()
      return
    case 'newtxt':
      newFile(true)
      return
    case 'newfile':
      newFile(false)
      return
  }
  if (!row) return
  switch (act) {
    case 'download':
      download(row)
      break
    case 'open':
      onDblClick(row)
      break
    case 'fav':
      toggleFav(rowAbs(row))
      break
    case 'edit':
      openEdit(row, rowAbs(row))
      break
    case 'copy':
      clipAddRows('copy', ctxRows(row))
      break
    case 'cut':
      clipAddRows('cut', ctxRows(row))
      break
    case 'archive':
      createArchive(ctxRows(row))
      break
    case 'archivedl':
      downloadArchive(ctxRows(row))
      break
    case 'unarchive':
      startUnarchive(row)
      break
    case 'props':
      openProps(row)
      break
    case 'rename':
      rename(row)
      break
    case 'remove':
      // 右键对象在多选里：整批走批量删除（同工具栏删除按钮）；否则单删
      if (ctxRows(row).length > 1) batchRemove(ctxRows(row))
      else removeOne(row)
      break
  }
}

// ---------- 复制 / 剪切 / 粘贴 ----------

// 剪贴板存在当前标签上（跨目录粘贴同一台服务器内有效），记下来源目录；
// 条目存来源绝对路径 from（搜索结果模式下条目不在当前目录，不能按 name 拼）
function clipAddRows(mode, rows) {
  if (!cur.value || !rows?.length) return
  cur.value.clip = { mode, dir: path.value, items: rows.map((r) => ({ name: r.name, from: rowAbs(r), isDir: r.is_dir })) }
  ElMessage.success(mode === 'copy' ? `已复制 ${rows.length} 项` : `已剪切 ${rows.length} 项`)
}

// 远端是否存在（size 对目录报「目标是目录」也代表存在；Stat 一次，开销小）
async function remoteExists(p) {
  try {
    await op({ op: 'size', path: p })
    return true
  } catch (e) {
    return /目标是目录/.test(e.message || '')
  }
}

async function pasteClip() {
  const t = cur.value
  if (!t?.clip?.items?.length) return
  const clip = t.clip
  let clash = false
  for (const it of clip.items) {
    if (joinOn(clip.dir, it.name) === join(it.name)) continue // 粘贴回原目录：跳过
    if (await remoteExists(join(it.name))) {
      clash = true
      break
    }
  }
  let force = false
  if (clash) {
    try {
      await ElMessageBox.confirm('目标目录存在同名项，覆盖？', '覆盖确认', {
        type: 'warning',
        confirmButtonText: '覆盖',
        cancelButtonText: '取消',
      })
      force = true
    } catch {
      return
    }
  }
  const verb = clip.mode === 'cut' ? '移动' : '粘贴'
  const fails = []
  for (const it of clip.items) {
    const from = it.from || joinOn(clip.dir, it.name) // 老条目无 from：按来源目录拼
    const to = join(it.name)
    if (from === to) continue
    try {
      await op({ op: clip.mode === 'cut' ? 'move' : 'copy', from, path: to, force })
    } catch (e) {
      fails.push(`${it.name}：${e.message}`)
    }
  }
  if (fails.length) ElMessage.warning(`部分${verb}失败：\n${fails.join('\n')}`)
  else ElMessage.success(`已${verb}`)
  if (clip.mode === 'cut' && !fails.length) t.clip = null
  refreshView()
}

// ---------- 后台任务（创建压缩包/解压）：进度轮询，完毕自动关窗 ----------

const taskDlg = reactive({ show: false, title: '', id: '', total: 0, done: 0, unit: '', status: 'running', error: '', timer: 0, sid: 0, canceling: false })

const taskPct = computed(() =>
  taskDlg.total > 0 ? Math.min(100, Math.round((taskDlg.done / taskDlg.total) * 100)) : 0,
)

// 进度副标题：字节数自适应单位（B/KB/MB…），zip 解压按条目数
const taskSubText = computed(() => {
  if (taskDlg.total <= 0) return '正在准备…'
  return taskDlg.unit === 'B'
    ? `${fmtBytes(taskDlg.done)} / ${fmtBytes(taskDlg.total)}`
    : `${taskDlg.done} / ${taskDlg.total} 项`
})

// 「后台运行」关掉进度窗后，工具栏仍留运行中徽标，点击重开进度窗
function reopenTask() {
  taskDlg.show = true
  if (!taskDlg.timer) taskDlg.timer = setInterval(pollTask, 500)
}

async function runTask(type, body, title) {
  const t = cur.value
  if (!t) return
  try {
    const r = await api.post(`/term/files/task?server_id=${t.id}`, { type, ...body })
    taskDlg.id = r.task?.id || ''
  } catch (e) {
    ElMessage.error(e.message)
    return
  }
  taskDlg.sid = t.id
  taskDlg.title = title
  taskDlg.total = 0
  taskDlg.done = 0
  taskDlg.unit = ''
  taskDlg.status = 'running'
  taskDlg.error = ''
  taskDlg.canceling = false
  taskDlg.show = true
  clearInterval(taskDlg.timer)
  taskDlg.timer = setInterval(pollTask, 500)
}

// 请求取消运行中的任务（协作式：服务端在当前读写边界退出，轮询感知 canceled）
async function cancelTask() {
  if (!taskDlg.id || taskDlg.canceling) return
  taskDlg.canceling = true
  try {
    await api.post(`/term/files/task-cancel?server_id=${taskDlg.sid}`, { id: taskDlg.id })
  } catch (e) {
    ElMessage.error(e.message) // 多为「任务已结束」（轮询马上会看到终态）
    taskDlg.canceling = false
  }
}

async function pollTask() {
  try {
    const r = await api.get(`/term/files/task?server_id=${taskDlg.sid}`)
    const t = r.task
    if (!t) return
    taskDlg.done = t.done
    taskDlg.total = t.total
    taskDlg.unit = t.unit || ''
    if (t.status === 'running') return
    clearInterval(taskDlg.timer)
    taskDlg.timer = 0
    taskDlg.canceling = false
    if (t.status === 'done') {
      taskDlg.status = 'done' // 徽标显示条件要求 running，不重置会一直挂着 100%
      taskDlg.show = false
      ElMessage.success(t.result ? `已创建 ${t.result}` : '解压完成')
      refreshView()
    } else if (t.status === 'canceled') {
      taskDlg.status = 'canceled'
      taskDlg.show = false
      ElMessage.info('任务已取消')
      refreshView()
    } else {
      taskDlg.status = 'error'
      taskDlg.error = t.error || '任务失败'
      if (!taskDlg.show) ElMessage.error(taskDlg.error)
    }
  } catch {
    /* 单次轮询失败忽略，下个周期再取 */
  }
}

onBeforeUnmount(() => {
  clearInterval(taskDlg.timer)
  stopPropsCount() // 组件销毁：停轮询并取消仍在跑的目录统计
})

// 解压入口（右键/双击共用）：先预检目标目录同名冲突，有则确认覆盖后再启动
async function startUnarchive(row) {
  const ap = rowAbs(row)
  try {
    const r = await api.post(`/term/files/unarchive-check?server_id=${cur.value.id}`, { path: ap })
    const conflicts = r.conflicts || []
    if (conflicts.length) {
      const more = conflicts.length >= 20 ? ' 等' : ''
      try {
        await ElMessageBox.confirm(
          `目标目录已存在同名项：${conflicts.join('、')}${more}。继续解压将覆盖，是否继续？`,
          '覆盖确认',
          { type: 'warning', confirmButtonText: '覆盖解压', cancelButtonText: '取消' },
        )
      } catch {
        return
      }
    }
  } catch {
    /* 预检失败不拦解压（读包失败等场景由解压任务自身报错） */
  }
  runTask('unarchive', { path: ap }, `解压 - ${row.name}`)
}

// ---------- 创建压缩包（归档+时间戳 tar.gz，存当前目录，同名自动加序号） ----------

// 压缩包基础名：单选取该文件/目录名（文件去扩展名）+时间戳，多选用 归档+时间戳
function archiveBase(rows) {
  const d = new Date()
  const p2 = (n) => String(n).padStart(2, '0')
  const ts = `${d.getFullYear()}${p2(d.getMonth() + 1)}${p2(d.getDate())}${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`
  if (rows?.length === 1) {
    const base = rows[0].is_dir ? rows[0].name : rows[0].name.replace(/\.[^./]+$/, '')
    return (base || '归档') + ts
  }
  return `归档${ts}`
}

function createArchive(rows) {
  if (!rows?.length) return
  runTask('archive', { paths: rows.map((x) => rowAbs(x)), name: archiveBase(rows) }, '创建压缩包')
}

// tar/tar.gz/tgz/zip 后缀判断（客户端预判菜单显隐；服务端有同款校验兜底）
function isArchiveName(name) {
  const l = (name || '').toLowerCase()
  return l.endsWith('.tar') || l.endsWith('.tar.gz') || l.endsWith('.tgz') || l.endsWith('.zip')
}

// ---------- 打包下载（选中项/目录服务端打为 tar.gz 流式返回，远端不落盘） ----------

function downloadArchive(rows) {
  if (!cur.value || !rows?.length) return
  const a = document.createElement('a')
  a.href = `${BASE}/term/files/archive?server_id=${cur.value.id}&paths=${encodeURIComponent(JSON.stringify(rows.map((r) => rowAbs(r))))}`
  a.download = 'files.tar.gz'
  a.click()
}

// ---------- 在线编辑（Monaco：语法高亮/错误标记/格式化/查找） ----------

const editDlg = reactive({ show: false, path: '', name: '', content: '', loading: false, saving: false, lang: 'plaintext' })
// 在线编辑未保存守卫（328）：内容与打开时快照不一致即视为脏
const editGuard = useDirtyGuard(() => ({ content: editDlg.content }))
const codeEd = ref(null)

const LANGS = [
  { v: 'plaintext', l: '纯文本' },
  { v: 'yaml', l: 'YAML' },
  { v: 'json', l: 'JSON' },
  { v: 'markdown', l: 'Markdown' },
  { v: 'javascript', l: 'JavaScript' },
  { v: 'typescript', l: 'TypeScript' },
  { v: 'xml', l: 'XML' },
  { v: 'html', l: 'HTML' },
  { v: 'css', l: 'CSS' },
  { v: 'python', l: 'Python' },
  { v: 'php', l: 'PHP' },
  { v: 'java', l: 'Java' },
  { v: 'shell', l: 'Shell' },
  { v: 'sql', l: 'SQL' },
  { v: 'ini', l: 'INI' },
]
const LANG_BY_EXT = {
  txt: 'plaintext', log: 'plaintext', text: 'plaintext',
  conf: 'ini', ini: 'ini', cfg: 'ini', env: 'ini', toml: 'ini',
  yml: 'yaml', yaml: 'yaml',
  json: 'json',
  md: 'markdown', markdown: 'markdown',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', tsx: 'typescript',
  xml: 'xml', svg: 'xml', plist: 'xml',
  html: 'html', htm: 'html', vue: 'html',
  css: 'css', scss: 'css', less: 'css',
  py: 'python', php: 'php', java: 'java',
  sh: 'shell', bash: 'shell', zsh: 'shell',
  sql: 'sql',
}
// 格式化依赖对应 language worker：只带 json worker（部署包精简砍了 css/html/js/ts
// 三个 worker），其余语法仍可编辑高亮、只是不支持自动格式化
const FMT_LANGS = new Set(['json'])

function detectLang(name) {
  const e = name.includes('.') ? name.split('.').pop().toLowerCase() : ''
  return LANG_BY_EXT[e] || 'plaintext'
}

async function openEdit(row, abs) {
  if (row.is_dir) return
  if ((row.size || 0) > 5 * 1024 * 1024) {
    ElMessage.warning('文件超过 5MB，不支持在线编辑')
    return
  }
  editDlg.path = abs || join(row.name)
  editDlg.name = row.name
  editDlg.content = ''
  editDlg.lang = detectLang(row.name)
  editDlg.show = true
  edPlain.value = false
  loadEdChunk()
  editDlg.loading = true
  try {
    const r = await op({ op: 'read', path: editDlg.path })
    // .txt 强制按文本打开（UTF-16 等编码的 txt 含 NUL 字节，会被误判成二进制）；
    // 其余文件含 NUL 仍视为二进制拒编辑
    const ext = row.name.includes('.') ? row.name.split('.').pop().toLowerCase() : ''
    if (ext !== 'txt' && (r.content || '').includes('\u0000')) {
      ElMessage.warning('检测到二进制内容，不支持在线编辑')
      editDlg.show = false
      return
    }
    editDlg.content = r.content || ''
    nextTick(() => editGuard.snapshot())
  } catch (e) {
    ElMessage.error(e.message)
    editDlg.show = false
  } finally {
    editDlg.loading = false
  }
}

async function saveEdit() {
  if (editDlg.saving) return
  editDlg.saving = true
  try {
    await op({ op: 'write', path: editDlg.path, content: editDlg.content })
    ElMessage.success('已保存')
    editDlg.show = false
    refreshView()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    editDlg.saving = false
  }
}

// capture 阶段接管 Ctrl/Cmd+S（Monaco 内部快捷键先于普通冒泡触发）
function onEditKeydown(e) {
  if ((e.ctrlKey || e.metaKey) && (e.key || '').toLowerCase() === 's') {
    e.preventDefault()
    e.stopPropagation()
    saveEdit()
  }
}

function formatEdit() {
  if (!FMT_LANGS.has(editDlg.lang)) {
    ElMessage.info('该语法暂不支持自动格式化（支持 JSON）')
    return
  }
  codeEd.value?.format()
}

function findInEdit() {
  codeEd.value?.find()
}

// ---------- 属性（「属性」基本信息 + 「权限」两个选项卡） ----------
// 目录不做自动统计，点「计算」才开始后台递归统计（个数/大小随扫描实时
// 增长）。counting = 统计任务进行中；counted = 已有实时数字；calcDone =
// 统计完成（大小定稿）；skipped = 无权限未计入的子目录数

const propsDlg = reactive({
  show: false, tab: 'info', path: '', name: '', isDir: false, mode: '',
  size: 0, mtime: 0, atime: 0,
  perm: 0o644, owner: '', ownerOrig: '', users: [], recursive: false,
  counting: false, counted: false, calcDone: false, files: 0, dirs: 0, rsize: 0, skipped: 0,
})
const permShifts = [
  { shift: 6, label: '所有者' },
  { shift: 3, label: '用户组' },
  { shift: 0, label: '公共' },
]

function hasPerm(shift, bit) {
  return ((propsDlg.perm >> shift) & bit) !== 0
}

function togglePerm(shift, bit) {
  propsDlg.perm = propsDlg.perm ^ (bit << shift)
}

const permText = computed({
  get: () => propsDlg.perm.toString(8).padStart(3, '0'),
  set: (v) => {
    const n = parseInt(v, 8)
    if (!Number.isNaN(n) && n >= 0 && n <= 0o777) propsDlg.perm = n
  },
})

const permOwnerOpts = computed(() => {
  const list = [...propsDlg.users]
  if (propsDlg.owner && !list.includes(propsDlg.owner)) list.unshift(propsDlg.owner)
  return list
})

function openProps(row) {
  propsDlg.tab = 'info'
  propsDlg.path = rowAbs(row)
  propsDlg.name = row.name
  propsDlg.isDir = row.is_dir
  propsDlg.mode = row.mode || ''
  propsDlg.size = row.size || 0
  propsDlg.mtime = row.mtime || 0
  propsDlg.atime = 0
  propsDlg.perm = parseInt(row.perm, 8) || 0
  propsDlg.owner = row.owner || ''
  propsDlg.ownerOrig = row.owner || ''
  propsDlg.recursive = false
  propsDlg.users = []
  propsDlg.counting = false
  propsDlg.counted = false
  propsDlg.calcDone = false
  propsDlg.files = 0
  propsDlg.dirs = 0
  propsDlg.rsize = 0
  propsDlg.skipped = 0
  propsDlg.show = true
  loadPropsTimes()
  op({ op: 'users' })
    .then((r) => (propsDlg.users = r.users || []))
    .catch(() => {
      /* 用户列表拿不到也不阻塞：当前所有者仍在下拉里可选 */
    })
}

// 访问时间：列表数据不带 atime，弹窗打开时单独取（SFTP 原生属性）；失败静默占位
async function loadPropsTimes() {
  try {
    const r = await op({ op: 'times', path: propsDlg.path })
    propsDlg.atime = r.atime || 0
  } catch {
    /* 拿不到就保持 '-' 占位 */
  }
}

function copyPropsPath() {
  copyText(propsDlg.path)
}

// 递归统计走后台任务（POST /term/files/count）：超大目录不再受 HTTP 超时
// 限制，个数/大小随扫描实时回传，可取消；启动失败（如该服务器正在统计）
// 不刷屏报错，留着「计算」按钮手动重试
let propsCountTimer = 0
let propsCountId = ''
let propsCountSid = 0

async function startPropsCount() {
  const t = cur.value
  if (!t) return
  try {
    const r = await api.post(`/term/files/count?server_id=${t.id}`, { path: propsDlg.path })
    propsCountId = r.task?.id || ''
    propsCountSid = t.id
  } catch {
    propsDlg.counting = false
    return // 启动失败：留给「计算」按钮重试
  }
  propsDlg.counting = true
  clearInterval(propsCountTimer)
  propsCountTimer = setInterval(pollPropsCount, 800)
  pollPropsCount()
}

async function pollPropsCount() {
  try {
    const r = await api.get(`/term/files/count?server_id=${propsCountSid}`)
    const t = r.task
    if (!t || t.type !== 'count') return
    const apply = () => {
      propsDlg.files = t.files || 0
      propsDlg.dirs = t.dirs || 0
      propsDlg.rsize = t.size || 0
      propsDlg.skipped = t.skipped || 0
    }
    if (t.status === 'running') {
      if (t.files || t.dirs) {
        apply() // 实时数字随扫描增长（空目录 0/0 等终态一次写齐）
        propsDlg.counted = true
      }
      return
    }
    clearInterval(propsCountTimer)
    propsCountTimer = 0
    propsDlg.counting = false
    if (t.status === 'done') {
      apply() // 空目录（0 文件 0 目录）也在终态写齐数字
      propsDlg.counted = true
      propsDlg.calcDone = true
    } else if (t.status === 'canceled') {
      propsDlg.counted = false // 取消：清掉实时数字，回「未计算」可重试
    } else if (t.status === 'error') {
      ElMessage.error(t.error || '统计失败') // 已流出的实时数字保留展示
    }
  } catch {
    /* 轮询失败：下个周期再试 */
  }
}

// 关弹窗即取消统计（协作式）；不取消的话大目录扫描会在后台跑到底白占连接
function stopPropsCount() {
  clearInterval(propsCountTimer)
  propsCountTimer = 0
  if (propsDlg.counting && propsCountId) {
    api.post(`/term/files/count-cancel?server_id=${propsCountSid}`, { id: propsCountId }).catch(() => {})
  }
  propsDlg.counting = false
}

async function applyPropsPerm() {
  const recursive = propsDlg.isDir && propsDlg.recursive
  try {
    await op({ op: 'chmod', path: propsDlg.path, perm: propsDlg.perm.toString(8), recursive })
  } catch (e) {
    ElMessage.error(e.message)
    return
  }
  if (propsDlg.owner && propsDlg.owner !== propsDlg.ownerOrig) {
    try {
      await op({ op: 'chown', path: propsDlg.path, owner: propsDlg.owner, recursive })
    } catch (e) {
      ElMessage.warning(`权限已更新，但修改所有者失败：${e.message}`)
    }
  }
  ElMessage.success('权限已更新')
  propsDlg.show = false
  refreshView()
}

// ---------- 文件操作 ----------

function validName(n) {
  if (!n || n.includes('/') || n === '.' || n === '..') {
    ElMessage.warning('名称不能为空且不能包含 /')
    return false
  }
  return true
}

async function op(body) {
  return api.post(`/term/files/op?server_id=${cur.value.id}`, body)
}

// asText=true 新建文本文件（无后缀自动补 .txt，建完直接打开编辑）
async function newFile(asText = false) {
  const tip = asText ? '在当前目录创建文本文件，创建后打开编辑' : '在当前目录创建空文件'
  let r
  try {
    r = await ElMessageBox.prompt(tip, asText ? '新建文本文件' : '新建文件', {
      closeOnClickModal: false,
      inputValue: '',
      confirmButtonText: '创建',
      cancelButtonText: '取消',
      inputPlaceholder: '文件名',
      inputValidator: (v) => (!v || v.includes('/') ? '名称不能为空且不能包含 /' : true),
    })
  } catch {
    return
  }
  let name = r.value.trim()
  if (asText && name && !name.includes('.')) name += '.txt'
  if (!validName(name)) return
  try {
    await op({ op: 'touch', path: join(name) })
    ElMessage.success('已创建')
    await refreshView()
    if (asText) openEdit({ name, is_dir: false, size: 0 })
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function newDir() {
  let r
  try {
    r = await ElMessageBox.prompt('在当前目录创建目录（含父目录）', '新建目录', {
      closeOnClickModal: false,
      inputValue: '',
      confirmButtonText: '创建',
      cancelButtonText: '取消',
      inputPlaceholder: '目录名',
      inputValidator: (v) => (!v || v.includes('/') ? '名称不能为空且不能包含 /' : true),
    })
  } catch {
    return
  }
  const name = r.value.trim()
  if (!validName(name)) return
  try {
    await op({ op: 'mkdir', path: join(name) })
    ElMessage.success('已创建')
    refreshView()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function rename(row) {
  // 系统目录保护与删除同口径：核心目录本身禁止重命名；其内部条目强提示后放行
  const abs = rowAbs(row)
  const scope = protectedScope(abs)
  if (scope?.self) {
    ElMessageBox.alert(`「${row.name}」是系统关键目录（${scope.dir}），禁止重命名`, '禁止操作', {
      type: 'error',
      confirmButtonText: '知道了',
    })
    return
  }
  const sysTip = scope ? `「${row.name}」位于系统目录 ${scope.dir} 下，重命名可能影响系统运行，请确认。\n` : ''
  let r
  try {
    r = await ElMessageBox.prompt(`${sysTip}重命名（同名目标将被覆盖，类 mv）`, `重命名 ${row.name}`, {
      closeOnClickModal: false,
      inputValue: row.name,
      confirmButtonText: '重命名',
      cancelButtonText: '取消',
      inputValidator: (v) => (!v || v.includes('/') ? '名称不能为空且不能包含 /' : true),
    })
  } catch {
    return
  }
  const name = r.value.trim()
  if (!validName(name) || name === row.name) return
  try {
    // 目标拼在条目所在目录（搜索结果模式下不一定等于当前目录）
    await op({ op: 'rename', path: abs, to: joinOn(parentPath(abs), name) })
    refreshView()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- 系统目录删除保护 ----------
// Linux 关键目录：目录本身禁止删除，其内部删除升级为强确认；
// /home /opt /srv /tmp 属用户自管区，不拦
const PROTECTED_DIRS = ['/', '/bin', '/sbin', '/usr', '/lib', '/lib64', '/lib32', '/libx32', '/boot', '/dev', '/etc', '/proc', '/sys', '/run', '/root', '/var']

// 规整路径：折叠 ./.. 段、去重复斜杠与尾部斜杠（根除外），便于与保护清单比对
function normPath(p) {
  const out = []
  for (const seg of p.split('/')) {
    if (!seg || seg === '.') continue
    if (seg === '..') {
      out.pop()
      continue
    }
    out.push(seg)
  }
  return '/' + out.join('/')
}

// 返回 { dir, self }：self=true 命中保护目录本身（禁止删除）；
// self=false 位于保护目录之下（强提示后放行）；null = 普通路径
function protectedScope(p) {
  const n = normPath(p)
  if (PROTECTED_DIRS.includes(n)) return { dir: n, self: true }
  for (const d of PROTECTED_DIRS) {
    if (d !== '/' && n.startsWith(d + '/')) return { dir: d, self: false }
  }
  return null
}

// 目录删除前先递归统计，确认框带上内容量（防呆）；统计失败不阻塞（服务端
// remove 本身是递归删除）。系统目录本身禁止删除，其内部升级为强确认
async function removeOne(row) {
  const abs = rowAbs(row)
  const scope = protectedScope(abs)
  if (scope?.self) {
    ElMessageBox.alert(`「${row.name}」是系统关键目录（${scope.dir}），禁止删除`, '禁止操作', {
      type: 'error',
      confirmButtonText: '知道了',
    })
    return
  }
  let tip = ''
  if (row.is_dir) {
    try {
      const c = await op({ op: 'count', path: abs })
      tip = `（含 ${c.files} 个文件、${c.dirs} 个子目录，共 ${fmtBytes(c.size)}）`
    } catch {
      /* 超大目录统计被拦也允许继续，确认框不带数量 */
    }
  }
  const sysTip = scope ? `「${row.name}」位于系统目录 ${scope.dir} 下，删除可能影响系统运行，请确认。\n` : ''
  try {
    await ElMessageBox.confirm(`${sysTip}确认删除 ${row.name}${tip}？删除后不可恢复`, scope ? '删除系统目录内容' : '删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await op({ op: 'remove', path: abs })
    ElMessage.success('已删除')
  } catch (e) {
    ElMessage.error(e.message)
  }
  refreshView()
}

// 多选批量删除：串行执行，失败汇总到一条消息；系统目录保护同单删口径
// rows 入参供右键菜单传入整批（右键对象在多选内时），缺省取当前勾选
async function batchRemove(rows = sel.value) {
  if (!rows.length) return
  const forbidden = rows.filter((r) => protectedScope(rowAbs(r))?.self)
  rows = rows.filter((r) => !forbidden.includes(r))
  if (forbidden.length) {
    ElMessageBox.alert(`系统关键目录不可删除，已跳过：${forbidden.map((r) => r.name).join('、')}`, '禁止操作', {
      type: 'error',
      confirmButtonText: '知道了',
    })
    if (!rows.length) return
  }
  const sysDirs = [...new Set(rows.filter((r) => protectedScope(rowAbs(r))).map((r) => protectedScope(rowAbs(r)).dir))]
  const sysTip = sysDirs.length ? `选中项位于系统目录（${sysDirs.join('、')}）下，删除可能影响系统运行，请确认。\n` : ''
  try {
    await ElMessageBox.confirm(`${sysTip}确认删除选中的 ${rows.length} 项？目录将递归删除，删除后不可恢复`, sysDirs.length ? '删除系统目录内容' : '批量删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  const fails = []
  for (const row of rows) {
    try {
      await op({ op: 'remove', path: rowAbs(row) })
    } catch (e) {
      fails.push(`${row.name}：${e.message}`)
    }
  }
  if (fails.length) ElMessage.warning(`部分删除失败：\n${fails.join('\n')}`)
  else ElMessage.success('已删除')
  refreshView()
}

// ---------- 文件内容搜索 ----------

const searchDlg = reactive({
  show: false, query: '', cs: false, regex: false,
  dir: '/', recursive: true, ext: '',
  busy: false, files: null, cur: null, scanned: 0, truncated: false,
})

function openSearch() {
  searchDlg.dir = path.value
  searchDlg.files = null
  searchDlg.cur = null
  searchDlg.show = true
}

const searchSummary = computed(() => {
  if (!searchDlg.files) return ''
  const n = searchDlg.files.length
  const hit = searchDlg.files.reduce((s, f) => s + f.count, 0)
  return `已扫描 ${searchDlg.scanned} 个文件${searchDlg.truncated ? '（达到上限，结果可能不全）' : ''}，${n} 个文件共 ${hit} 处命中`
})

async function doSearch() {
  const q = searchDlg.query.trim()
  if (!q) {
    ElMessage.warning('请输入搜索内容')
    return
  }
  searchDlg.busy = true
  try {
    const r = await api.post(`/term/files/search?server_id=${cur.value.id}`, {
      path: searchDlg.dir || '/',
      query: q,
      regex: searchDlg.regex,
      case_sensitive: searchDlg.cs,
      recursive: searchDlg.recursive,
      ext: searchDlg.ext,
    })
    searchDlg.files = r.files || []
    searchDlg.scanned = r.scanned || 0
    searchDlg.truncated = !!r.truncated
    searchDlg.cur = searchDlg.files[0] || null
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    searchDlg.busy = false
  }
}

function editFromSearch(f) {
  searchDlg.show = false
  openEdit({ name: f.name, is_dir: false, size: 0 }, f.path)
}

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))
}

// 命中行高亮：按当前搜索条件切分原文，转义后拼 <mark>（防内容注入）
function hlLine(text) {
  const q = searchDlg.query.trim()
  if (!q) return escapeHtml(text)
  let re
  try {
    const src = searchDlg.regex ? q : q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    re = new RegExp(src, 'g' + (searchDlg.cs ? '' : 'i'))
  } catch {
    return escapeHtml(text)
  }
  const spans = []
  let m
  let last = 0
  while ((m = re.exec(text))) {
    if (m.index > last) spans.push({ t: text.slice(last, m.index), hit: false })
    const len = m[0].length
    spans.push({ t: len ? m[0] : text[m.index] || '', hit: true })
    last = m.index + (len || 1)
    if (!len) re.lastIndex++
    if (spans.length > 60) break
  }
  if (last < text.length) spans.push({ t: text.slice(last), hit: false })
  return spans.map((s) => (s.hit ? '<mark>' + escapeHtml(s.t) + '</mark>' : escapeHtml(s.t))).join('')
}

// ---------- 上传 / 下载（进度 + 速度 + 取消确认 + 断点续传） ----------

// 任务对象必须用 reactive() 包装后再 push：闭包（进度回调）持有的是对象引用，
// 若是原始对象，写入会绕过 Vue 的响应式代理——进度条/速度永远不更新（此前
// 「进度条不动」的真正根因）
const transfers = ref([])
let xferSeq = 0
const activeCount = computed(() => transfers.value.filter((t) => t.status === 'active').length)

// 进度回调：loaded/total 为全文件口径（续传时已含偏移）。速度用相邻两次
// 采样的瞬时值做 EMA 平滑（进度事件抖动大，直接除会出现 0 → 峰值跳变）
function setProg(tr, loaded, total) {
  const now = Date.now()
  const dt = (now - tr.lastTs) / 1000
  if (tr.lastTs && dt >= 0.2) {
    const inst = (loaded - tr.lastLoaded) / dt
    if (inst >= 0) tr.speed = tr.speed ? tr.speed * 0.7 + inst * 0.3 : inst
    tr.lastTs = now
    tr.lastLoaded = loaded
  }
  tr.loaded = loaded
  if (total) tr.total = total
  tr.pct = tr.total ? Math.min(100, Math.floor((loaded / tr.total) * 100)) : 0
}

// 传输条副标题：进行中显示 已传/总量 · 实时速度；终态显示结果文案
function xferSub(tr) {
  if (tr.status === 'active') {
    const spd = tr.speed > 0 ? ` · ${fmtBytes(tr.speed)}/s` : ''
    return `${fmtBytes(tr.loaded)} / ${fmtBytes(tr.total)}${spd}`
  }
  if (tr.status === 'success') return `已完成 · 共 ${fmtBytes(tr.total)}`
  return tr.statusText || `${fmtBytes(tr.loaded)} / ${fmtBytes(tr.total)}`
}

// 取消需确认：远端 .lzpart 临时文件保留，可续传接着传
async function cancelXfer(tr) {
  try {
    await ElMessageBox.confirm(`取消「${tr.name}」？已传输的部分会保留，可在上传列表点「续传」继续。`, '取消上传', {
      type: 'warning',
      confirmButtonText: '取消上传',
      cancelButtonText: '继续上传',
      closeOnClickModal: false,
    })
  } catch {
    return
  }
  tr.xhr?.abort?.()
}

function pickUpload() {
  upInput.value?.click()
}

async function onUploadPicked(ev) {
  const fs = [...ev.target.files]
  ev.target.value = ''
  await uploadFiles(fs)
}

// 统一上传入口（按钮选择 + 本地拖拽共用）：串行逐个上传——避免多路大文件
// 写满目标机磁盘/带宽；当前目录已有同名文件时先确认覆盖（服务端 Create 为
// 截断覆盖语义），取消则跳过该文件继续传其余
async function uploadFiles(fs) {
  if (!cur.value || !fs.length) return
  for (const f of fs) {
    if (files.value.some((x) => !x.is_dir && x.name === f.name)) {
      try {
        await ElMessageBox.confirm(`当前目录已存在同名文件「${f.name}」，覆盖上传？`, '覆盖确认', {
          type: 'warning',
          confirmButtonText: '覆盖',
          cancelButtonText: '跳过',
          closeOnClickModal: false,
        })
      } catch {
        continue
      }
    }
    await uploadOne(f)
  }
  refreshView()
}

// ---------- 本地拖拽上传：拖入文件区高亮，松手即上传到当前目录 ----------

const dragOver = ref(false)

function onDragOver(ev) {
  if (!cur.value || !ev.dataTransfer || ![...ev.dataTransfer.types].includes('Files')) return
  dragOver.value = true
}

function onDragLeave(ev) {
  if (!ev.currentTarget.contains(ev.relatedTarget)) dragOver.value = false
}

async function onDrop(ev) {
  dragOver.value = false
  if (!cur.value || !ev.dataTransfer) return
  const fs = [...ev.dataTransfer.files]
  if (!fs.length) return
  // 目录检测用 items 的 entry（File 对象本身没有可靠的 isDirectory 语义；
  // entry 必须在事件派发的同步阶段取，await 之后 DataTransferItemList 失效）
  const entries = [...ev.dataTransfer.items].map((it) => it.webkitGetAsEntry?.())
  const dirs = fs.filter((f, i) => entries[i]?.isDirectory)
  if (dirs.length) {
    ElMessage.warning(`无法传输文件夹：${dirs.map((d) => d.name).join('、')}`)
  }
  const files = fs.filter((f, i) => !entries[i]?.isDirectory)
  if (files.length) await uploadFiles(files)
}

// opOn 在指定服务器上执行文件操作（续传可能发生在切换服务器之后，
// 不能用当前 cur）；op 仍面向当前服务器
function opOn(sid, body) {
  return api.post(`/term/files/op?server_id=${sid}`, body)
}

// 新建上传任务：reactive() 包装——原始对象 push 进数组后，闭包若持原始
// 引用写入会绕过代理不触发渲染；任务持有 file/xhr/sid/dir/rpath，
// 切换服务器/目录后依然可续传
function newXfer(name, total) {
  const tr = reactive({
    key: ++xferSeq, name, loaded: 0, total, pct: 0,
    status: 'active', statusText: '', speed: 0, lastTs: 0, lastLoaded: 0,
    xhr: null, file: null,
    sid: cur.value.id, dir: path.value, rpath: '',
  })
  transfers.value.push(tr)
  return tr
}

function settle(tr, e) {
  if (e?.message === '已取消') {
    tr.status = 'canceled'
    tr.statusText = '已取消'
  } else {
    tr.status = 'exception'
    tr.statusText = e?.message || '上传失败'
    ElMessage.error(`${tr.name}：${tr.statusText}`)
  }
}

// 分片上传偏移重对齐：查远端 .lzpart 实际大小（不小于本地视为残片异常，从头传）
function lzpartRealign(tr) {
  return async () => {
    try {
      const sz = (await opOn(tr.sid, { op: 'size', path: tr.rpath + '.lzpart' })).size || 0
      return sz >= tr.file.size ? 0 : sz
    } catch {
      return 0
    }
  }
}

async function uploadOne(f) {
  const tr = newXfer(f.name, f.size)
  tr.file = f
  tr.rpath = join(f.name)
  try {
    await uploadFileChunked(tr.sid, tr.dir, f, {
      onProgress: (ld, tot) => setProg(tr, ld, tot),
      onReady: (xhr) => (tr.xhr = xhr),
      realign: lzpartRealign(tr),
    })
    tr.status = 'success'
    tr.pct = 100
  } catch (e) {
    settle(tr, e)
  }
}

// 下载：浏览器原生下载（<a> 导航，落盘到浏览器下载目录，暂停/续传/断点
// 由浏览器下载器管理，页面刷新不丢进度）。带不了 Authorization 头——
// 鉴权走登录时种下的 HttpOnly Cookie（见服务端 auth.go）
function download(row) {
  const a = document.createElement('a')
  a.href = `${BASE}/term/files/download?server_id=${cur.value.id}&path=${encodeURIComponent(rowAbs(row))}`
  a.download = row.name
  a.click()
}

// 断点续传：先查远端临时文件已有大小对齐偏移（残片没了/比本地大则从头传）。
// 成功与否之外的状态下远端都留着 <name>.lzpart，直到续传完成或清除记录
async function resumeXfer(tr) {
  if (tr.status === 'active') return // 双击防护：两条链路交错写同一 .lzpart 必互相挤偏移
  tr.status = 'active'
  tr.statusText = ''
  tr.speed = 0
  tr.lastTs = 0
  tr.lastLoaded = 0
  try {
    let offset = 0
    try {
      offset = (await opOn(tr.sid, { op: 'size', path: tr.rpath + '.lzpart' })).size || 0
    } catch {
      offset = 0 // 远端临时文件已不存在：从头传
    }
    if (offset >= tr.total) offset = 0 // 比本地大/恰好传完：覆盖重传
    await uploadFileChunked(tr.sid, tr.dir, tr.file, {
      onProgress: (ld, tot) => setProg(tr, ld, tot),
      onReady: (xhr) => (tr.xhr = xhr),
      offset,
      realign: lzpartRealign(tr),
    })
    tr.status = 'success'
    tr.pct = 100
  } catch (e) {
    settle(tr, e)
  }
}

// 清除全部记录；已取消/失败的上传连带删掉远端 .lzpart 残片（尽力而为，
// 删不掉说明远端已不可达，残片留在远端不影响正式文件）
async function clearDone() {
  const stale = transfers.value.filter((t) => t.status !== 'success' && t.rpath)
  for (const t of stale) {
    try {
      await opOn(t.sid, { op: 'remove', path: t.rpath + '.lzpart' })
    } catch {
      /* 远端清理失败不阻塞本地清除 */
    }
  }
  transfers.value = []
}

// ---------- 生命周期 ----------

async function load() {
  try {
    const [res, gres] = await Promise.all([api.get('/servers'), api.get('/groups')])
    servers.value = res.servers || []
    groups.value = gres.groups || []
    // 服务器被删后清掉对应标签
    for (const t of [...tabs.value]) {
      if (!servers.value.find((s) => s.id === t.id)) closeTab(t)
    }
  } catch (e) {
    ElMessage.error(e.message)
  }
}

onMounted(load)
let filesMountedJustNow = true
onActivated(() => {
  if (filesMountedJustNow) {
    filesMountedJustNow = false
    return
  }
  load()
})
</script>

<style scoped>
.files-page {
  display: flex;
  /* 侧栏与主区间距=分隔条自身 8px（可拖拽调宽、单击收起，见 useSidePanel） */
  gap: 0;
  min-height: 0;
  flex-direction: column;
  height: calc(100vh - 130px); /* 顶栏 + 标签栏 */
}
@media (min-width: 769px) {
  .files-page {
    flex-direction: row;
  }
}
@media (max-width: 768px) {
  .files-page {
    height: calc(100vh - 110px);
    gap: 8px; /* 手机端无分隔条（已隐藏），上下堆叠留常规间距 */
  }
}

/* 主区 */
.files-main {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 服务器标签栏（胶囊样式，与文档页一致：选中实底品牌色） */
.tab-bar {
  flex: none;
  display: flex;
  gap: 6px;
  overflow-x: auto;
  padding-bottom: 2px;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  font-size: 13px;
  color: var(--text-2);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
  user-select: none;
}
.tab:hover {
  color: var(--ink);
  border-color: var(--brand);
}
.tab.on {
  color: #fff;
  background: var(--brand);
  border-color: var(--brand);
  font-weight: 600;
}
.tab.on .tab-close {
  color: rgba(255, 255, 255, 0.85);
}
.tab.on .tab-close:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.2);
}
.tab-close {
  border-radius: 4px;
  font-size: 12px;
  color: var(--muted);
}
.tab-close:hover {
  background: var(--border);
  color: var(--danger, #f56c6c);
}

/* 工具栏两行（参考宝塔布局）：第一行导航（向上/后退+路径框+刷新+名称搜索），
   第二行操作按钮组；相邻 Element 按钮自带 margin-left:12px 会叠在 flex gap
   上，两行容器统一清零只留 gap */
.fm-nav {
  flex: none;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.fm-nav :deep(.el-button + .el-button) {
  margin-left: 0;
}
.fm-toolbar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.fm-toolbar :deep(.el-button + .el-button) {
  margin-left: 0;
}
/* 下拉按钮的小箭头 */
.tb-arr {
  margin-left: 2px;
  font-size: 12px;
  opacity: 0.75;
}
/* 导航行：向上/后退两键成组（间距收紧） */
.tb-nav {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
.tb-nav :deep(.el-button + .el-button) {
  margin-left: 0;
}
/* 名称搜索组：靠右；不勾子目录=当前目录即时过滤，勾上回车=递归搜索 */
.tb-find {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}
.find-input {
  width: 210px;
}
.find-sub {
  margin-right: 0;
  white-space: nowrap;
}
@media (max-width: 768px) {
  .find-input {
    width: 140px;
  }
}
/* 任务徽标区（后台任务运行中显示，靠右） */
.tb-more {
  margin-left: auto;
  display: flex;
  align-items: center;
}
.tb-more :deep(.el-button + .el-button) {
  margin-left: 0;
}
/* 子目录搜索结果条 */
.find-bar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 12px;
  font-size: 13px;
  color: var(--text-2);
  background: var(--el-color-primary-light-9, #ecf5ff);
  border: 1px solid var(--el-color-primary-light-7, #d9ecff);
  border-radius: 6px;
}
.fb-ico {
  color: var(--el-color-primary);
}
.fb-txt {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fb-dir {
  color: var(--muted);
}
.fb-clear {
  flex: none;
}
/* 路径框：常态显示面包屑（可点击），聚焦变输入框，右侧复制按钮 */
.path-box {
  position: relative;
  flex: 1 1 300px;
  min-width: 200px;
  max-width: 560px;
  display: flex;
  align-items: center;
  height: 26px;
  padding-right: 26px;
  border: 1px solid var(--border, #dcdfe6);
  border-radius: 6px;
  background: var(--surface);
  font-size: 13px;
}
.path-box:focus-within {
  border-color: var(--el-color-primary);
}
.path-box:not(.editing) .path-input {
  color: transparent;
  caret-color: transparent;
}
.path-input {
  flex: 1;
  min-width: 0;
  height: 100%;
  padding: 0 4px 0 8px;
  border: none;
  outline: none;
  background: transparent;
  font-size: 13px;
  color: var(--ink, #303133);
}
.crumbs {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  padding-left: 8px;
  overflow: hidden;
  white-space: nowrap;
  pointer-events: none; /* 空白处点击穿透到输入框（聚焦可编辑），面包屑可点 */
}
.crumb {
  pointer-events: auto;
  padding: 1px 4px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--ink, #303133); /* --text 未定义（曾回落深灰，violet 紫底上看不清，263） */
  white-space: nowrap;
  flex: none;
}
.crumb:hover {
  background: var(--el-fill-color-light);
  color: var(--el-color-primary);
}
.crumb.cur {
  color: var(--el-color-primary);
  font-weight: 600;
}
.crumb-sep {
  color: var(--muted);
  flex: none;
}
.path-copy {
  position: absolute;
  right: 6px;
  display: flex;
  color: var(--muted);
  cursor: pointer;
}
.path-copy:hover {
  color: var(--el-color-primary);
}

/* 收藏夹弹层（el-popover 内容随组件 scoped 生效） */
.fav-cur {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  font-size: 13px;
  border-radius: 4px;
  cursor: pointer;
}
.fav-cur:hover {
  background: var(--el-fill-color-light);
  color: var(--el-color-primary);
}
.fav-sep {
  height: 1px;
  margin: 4px 0;
  background: var(--border, #e4e7ed);
}
.fav-scroll {
  max-height: 280px;
  overflow-y: auto;
}
.fav-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  font-size: 13px;
  border-radius: 4px;
  cursor: pointer;
}
.fav-item:hover {
  background: var(--el-fill-color-light);
}
.fav-ico {
  flex: none;
  color: var(--brand);
}
.fav-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fav-del {
  flex: none;
  color: var(--muted);
}
.fav-del:hover {
  color: var(--el-color-danger, #f56c6c);
}
.fav-empty {
  padding: 8px 4px 4px;
  font-size: 12px;
  color: var(--muted);
  text-align: center;
}
.tb-task-pct {
  margin-left: 2px;
  font-size: 12px;
}
.fm-table {
  flex: 1;
  min-height: 0;
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 8px;
  /* 加重：经 Element 的 CSS 变量覆盖默认浅色（比逐 .cell 写色稳，
     不受组件内部优先级影响）；行线比默认描边深一档，明暗各自取值 */
  --el-table-text-color: var(--ink);
  --el-table-header-text-color: var(--text-2);
  --el-table-row-hover-bg-color: rgba(61, 139, 221, 0.12);
  /* 列头底色：商务蓝（比面板深一档、清爽不闷，与品牌蓝同系） */
  --el-table-header-bg-color: #d7e6f8;
  --fm-row-line: #c9ccd2;
}
html.dark .fm-table {
  --el-table-header-bg-color: #1d2a3a;
  --fm-row-line: #3a3f45;
}
.fm-table :deep(.el-table__body .cell) {
  font-size: 15px;
}
/* 行高 +20%（EP small 默认上下内边距 4px→7.2px：23px 行距+8px 边距 ≈32px 行高） */
.fm-table :deep(.el-table__body td.el-table__cell) {
  padding: 7.2px 0;
}
.fm-table :deep(.el-table__header .cell) {
  font-size: 14px;
  font-weight: 600;
}
.fm-table :deep(.el-table td.el-table__cell),
.fm-table :deep(.el-table th.el-table__cell.is-leaf) {
  border-bottom: 1px solid var(--fm-row-line);
}
/* 全部列单行不折行（超长省略，列宽可拖拽调）；!important 压死 EP
   .el-table .cell 的 white-space:normal（时间列含空格最易被换行） */
.fm-table :deep(.el-table .cell) {
  white-space: nowrap !important;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 复选框加大一点（默认 14px → 16px；勾标 top/left 50% 自居中，只需放大
   宽高；半选横线原 top:5px 按新高度居中改 7px）。描边比 Element 默认
   （#dcdfe6）加深一档，白底上看得清 */
.fm-table :deep(.el-checkbox__inner) {
  width: 16px;
  height: 16px;
  border-color: #909399;
}
html.dark .fm-table :deep(.el-checkbox__inner) {
  border-color: #6a7078;
}
.fm-table :deep(.el-checkbox__inner::after) {
  width: 4px;
  height: 8px;
}
.fm-table :deep(.el-checkbox__input.is-indeterminate .el-checkbox__inner::before) {
  top: 7px;
}
/* 选中行底色加深（品牌蓝 32% 叠底）；悬停轻度着色（不覆盖选中） */
.fm-table :deep(tr.sel-row > td.el-table__cell) {
  background: rgba(61, 139, 221, 0.32) !important;
}
.fm-table :deep(tr.sel-row:hover > td.el-table__cell) {
  background: rgba(61, 139, 221, 0.4) !important;
}
.fm-table :deep(.el-table__body tr:hover > td.el-table__cell) {
  background: var(--el-table-row-hover-bg-color) !important;
}
/* 操作列：图标按钮右对齐、单行不换行 */
.fm-table :deep(.ops-col .cell) {
  white-space: nowrap;
}
.fm-table :deep(.ops-col .el-button) {
  font-size: 16px;
}
/* 名称列悬停手形（可点击，进目录/开编辑） */
.fm-table :deep(.name-col) {
  cursor: pointer;
}
/* 列宽拖拽把手：表头右缘 9px 热区 */
.fm-table :deep(.el-table__header-wrapper thead th) {
  position: relative;
}
.fm-table :deep(.th-drag) {
  position: absolute;
  top: 0;
  right: -4px;
  width: 9px;
  height: 100%;
  cursor: col-resize;
  z-index: 3;
}
.fm-table :deep(.th-drag:hover) {
  background: rgba(77, 141, 245, 0.3);
}
.f-icon {
  vertical-align: -4px;
  margin-right: 6px;
  font-size: 20px;
}
.f-name {
  vertical-align: middle;
}
/* 时间列单行兜底（无其他规则命中 .f-time，与 .cell 的 nowrap 双保险）；
   默认列宽 190 保证 19 字符时间串在 15px 字号 + .cell 24px 内边距下放得下 */
.f-time {
  white-space: nowrap;
  vertical-align: middle;
}
/* 拖拽上传：拖入文件区整体虚线高亮 */
.files-main.drop-over {
  outline: 2px dashed var(--brand, #4d8df5);
  outline-offset: -4px;
  border-radius: 8px;
}
.fm-status {
  flex: none;
  display: flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
}
.st-path {
  overflow: hidden;
  text-overflow: ellipsis;
  direction: rtl; /* 长路径截左边，保留尾部当前目录 */
  text-align: left;
  flex: 1;
  min-width: 60px;
}
/* 磁盘使用（根分区）：右对齐 进度条 */
.st-disk {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: none;
}
.st-disk-bar {
  width: 140px;
}
.fm-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 8px;
  font-size: 14px;
}
.fm-empty .el-icon {
  font-size: 34px;
  opacity: 0.5;
}
.fm-empty .sub {
  font-size: 12px;
  opacity: 0.8;
}
/* 中转流量警示：红字显著提示（云主机流量计费场景） */
.fm-empty .relay-warn {
  margin-top: 10px;
  max-width: 480px;
  font-size: 13px;
  font-weight: 600;
  color: var(--crit, #d92d20);
  line-height: 1.7;
}

/* 右键菜单（分组） */
.ctx-backdrop {
  position: fixed;
  inset: 0;
  z-index: 2000;
}
.ctx-menu {
  position: fixed;
  z-index: 2001;
  min-width: 150px;
  padding: 4px;
  background: var(--surface);
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 8px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.14);
}
.ctx-sep {
  height: 1px;
  margin: 4px 8px;
  background: var(--border, #e4e7ed);
}
.ctx-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 12px;
  border-radius: 5px;
  font-size: 13px;
  cursor: pointer;
  user-select: none;
}
.ctx-item:hover {
  background: var(--el-fill-color-light);
  color: var(--el-color-primary);
}
.ctx-item.danger:hover {
  background: var(--el-color-danger-light-9, #fef0f0);
  color: var(--el-color-danger, #f56c6c);
}
.ctx-item.disabled {
  opacity: 0.45;
}

/* 在线编辑 */
.ed-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.ed-lang {
  width: 150px;
}
.ed-tip {
  margin-left: auto;
  font-size: 12px;
  color: var(--muted);
}
.ed-body {
  height: 62vh;
}
.ed-code {
  height: 100%;
}
/* 编辑器 chunk 加载中/加载失败的占位提示（居中，占满 .ed-body） */
.ed-chunk-tip {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
  font-size: 13px;
}
.ed-chunk-spin {
  font-size: 26px;
  color: var(--brand, #409eff);
}
.ed-chunk-btns {
  display: flex;
  gap: 10px;
}
/* 简易编辑兜底：纯 textarea 占满编辑区（手机端 Monaco 加载失败仍可查看/保存） */
.ed-plain {
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.ed-plain-ta {
  flex: 1;
}
.ed-plain-ta :deep(.el-textarea__inner) {
  height: 100%;
  font-family: var(--mono, 'SFMono-Regular', Consolas, monospace);
}
.ed-plain-tip {
  font-size: 12px;
  color: var(--muted);
}
.edit-tip {
  float: left;
  font-size: 12px;
  color: var(--muted);
  line-height: 32px;
}
/* 图片预览弹层：居中显示、按窗口等比缩放 */
.img-view {
  display: flex;
  justify-content: center;
  min-height: 120px;
}
.img-view :deep(.el-image__inner) {
  max-width: 100%;
  max-height: 68vh;
}
.img-err {
  padding: 48px 24px;
  color: var(--muted);
  font-size: 13px;
}

/* 属性弹窗 */
.pp-grid {
  display: grid;
  grid-template-columns: 72px 1fr;
  row-gap: 10px;
  column-gap: 10px;
  font-size: 13px;
  padding-top: 4px;
}
.pp-k {
  color: var(--muted);
}
.pp-v {
  word-break: break-all;
}
.pp-path {
  word-break: break-all;
}
.pp-copy {
  cursor: pointer;
  color: var(--text-2);
  margin-left: 6px;
  vertical-align: -2px;
}
.pp-copy:hover {
  color: var(--brand);
}
.pp-calc {
  margin-left: 8px;
}
.pp-wait {
  color: var(--muted);
}
/* 权限选项卡：三组「标题嵌边框」盒子（读/写/执行竖排）居中排布，底部数字+所有者+应用 */
.perm-boxes {
  display: flex;
  justify-content: center;
  gap: 18px;
  flex-wrap: wrap;
  padding: 10px 2px 2px;
}
.perm-box {
  position: relative;
  min-width: 136px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 16px 18px 10px;
  border: 1px solid var(--border, #dcdfe6);
  border-radius: 6px;
}
.perm-box-title {
  position: absolute;
  top: -9px;
  left: 12px;
  padding: 0 6px;
  background: var(--el-dialog-bg-color, var(--surface));
  font-size: 12px;
  color: var(--text-2);
}
.perm-box :deep(.el-checkbox) {
  height: 26px;
  margin-right: 0;
}
.perm-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 16px;
  flex-wrap: wrap;
}
.perm-num {
  width: 84px;
}
.perm-owner {
  width: 150px;
}
.perm-hint {
  font-size: 13px;
  color: var(--text-2);
}

/* 后台任务进度 */
.task-sub {
  margin-top: 8px;
  font-size: 12px;
  color: var(--muted);
  text-align: center;
}
.task-err {
  font-size: 13px;
  color: var(--el-color-danger, #f56c6c);
  word-break: break-all;
}

/* 内容搜索 */
.s-form {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 10px;
}
.s-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.s-lab {
  flex: none;
  width: 62px;
  font-size: 13px;
  color: var(--muted);
  text-align: right;
}
.s-query {
  flex: 1;
}
.s-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  color: var(--muted);
  padding: 6px 2px;
  border-bottom: 1px solid var(--border, #e4e7ed);
}
.s-body {
  display: flex;
  height: 46vh;
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 6px;
  overflow: hidden;
}
.s-files {
  width: 240px;
  flex: none;
  overflow-y: auto;
  border-right: 1px solid var(--border, #e4e7ed);
  padding: 4px;
}
.s-file {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 6px 8px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
.s-file:hover {
  background: var(--el-fill-color-light);
}
.s-file.on {
  background: var(--el-color-primary-light-9, #ecf5ff);
}
.s-fname {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.s-none {
  padding: 18px 10px;
  font-size: 12px;
  color: var(--muted);
  text-align: center;
}
.s-lines {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 6px 10px;
  font-size: 12px;
}
.s-lhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding-bottom: 6px;
  margin-bottom: 6px;
  border-bottom: 1px dashed var(--border, #e4e7ed);
}
.s-lpath {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  direction: rtl;
  text-align: left;
}
.s-line {
  padding: 3px 0;
  line-height: 1.6;
  word-break: break-all;
  border-bottom: 1px dashed var(--border, #ebeef5);
}
.s-no {
  color: var(--muted);
}
.s-txt :deep(mark) {
  background: #ffe27a;
  color: inherit;
  padding: 0 1px;
  border-radius: 2px;
}

/* 传输面板 */
.xfers {
  position: fixed;
  right: 18px;
  bottom: 18px;
  z-index: 1500;
  width: 300px;
  max-height: 42vh;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 10px;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.16);
  overflow: hidden;
}
/* violet：白透玻璃底太透、浮层内容与页面重叠看不清（264）——深紫磨砂实底 */
html.dark.violet .xfers {
  background: rgba(32, 24, 66, 0.96);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border-color: rgba(255, 255, 255, 0.2);
  box-shadow: 0 8px 32px rgba(15, 8, 40, 0.5);
}
.xfers-head {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  font-size: 13px;
  font-weight: 600;
  border-bottom: 1px solid var(--border, #e4e7ed);
}
.xfers-list {
  overflow-y: auto;
  padding: 6px 10px;
}
.xfer {
  display: flex;
  gap: 8px;
  padding: 7px 2px;
}
.xfer + .xfer {
  border-top: 1px dashed var(--border, #ebeef5);
}
.x-ico {
  flex: none;
  margin-top: 2px;
  font-size: 16px;
  color: var(--el-color-primary, #4d8df5);
}
.x-main {
  flex: 1;
  min-width: 0;
}
.x-name {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.x-sub {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--muted);
  margin-top: 2px;
}
.x-cancel {
  color: var(--el-color-primary);
  cursor: pointer;
}
.x-cancel:hover {
  text-decoration: underline;
}
</style>
